// 本文件实现 client 的本地 IPC 数据平面，把收到的 MIDI 消息额外发布到一个 TCP socket 上供其他进程读取
// 输出开关由 ipc.listen 配置决定，为空时不创建监听器，调用方的相关方法在 nil 接收者上安全返回
// 帧格式为 [uint32 LE length][uint8 kind][body...]，length 统计 kind 与 body 的字节数，不包含 length 字段自身
// 本通道只读，服务端不读取消费者发来的数据，也不做鉴权，访问边界由使用者选择的绑定地址决定
//
// This file implements the client's local IPC data plane, publishing received MIDI messages on a TCP socket for other processes
// The output is controlled by the ipc.listen setting; when it is empty no listener is created and the related methods return safely on a nil receiver
// The frame format is [uint32 LE length][uint8 kind][body...], where length counts the kind byte and the body but not the length field itself
// The channel is read-only, the server never reads from consumers and performs no authentication, and the reach of the interface follows the bind address the user chooses
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	golog "github.com/donnie4w/go-logger/logger"
)

const (
	// ipcKindControlJSON 控制帧，body 为 UTF-8 JSON
	// ipcKindControlJSON marks a control frame whose body is UTF-8 JSON
	ipcKindControlJSON byte = 0x01
	// ipcKindMIDI 数据帧，body 为一条完整的 MIDI 消息
	// ipcKindMIDI marks a data frame whose body is one complete MIDI message
	ipcKindMIDI byte = 0x02
	// ipcMaxFrameSize 单帧上限，容纳最大 64 KiB 的 SysEx 后仍有充裕余量
	// ipcMaxFrameSize caps one frame and leaves ample headroom over the 64 KiB maximum SysEx payload
	ipcMaxFrameSize = 1 << 20
	// ipcMaxConnections 并发消费者上限，限制本机进程的占用
	// ipcMaxConnections caps concurrent consumers so local processes cannot take unbounded slots
	ipcMaxConnections = 8
	// ipcSendQueueSize 每个连接的发送队列深度，队列满时丢弃新帧以避免阻塞 Publish 的调用方
	// ipcSendQueueSize is the per-connection send queue depth; when full the new frame is dropped so the Publish caller is not blocked
	ipcSendQueueSize = 2048
	// ipcDropLogInterval 丢弃告警的日志间隔，避免 MIDI 速率下日志量过大
	// ipcDropLogInterval spaces drop warnings apart so the log volume stays bounded at MIDI rates
	ipcDropLogInterval = 500
	// ipcClientName 握手帧中报告的实现名
	// ipcClientName is the implementation name reported in the handshake frame
	ipcClientName = "midibridge-client"
	// ipcProtoVersion 协议版本，遵循语义化版本，消费者据此判断兼容性
	// ipcProtoVersion is the protocol version in semantic form, which consumers use to judge compatibility
	ipcProtoVersion = "1.0.0"
)

// ipcHello 是连接建立后立即发送的控制帧 body
// ipcHello is the body of the control frame sent immediately after a connection is established
type ipcHello struct {
	Type    string `json:"type"`
	Proto   string `json:"proto"`
	Client  string `json:"client"`
	Version string `json:"version"`
}

// ipcConn 表示一个已接入的消费者连接
// ipcConn represents one connected consumer
type ipcConn struct {
	// srv 指回所属服务端，供写协程在退出时注销自己
	// srv points back to the owning server so the write goroutine can deregister itself on exit
	srv  *IPCServer
	conn net.Conn
	// send 存放已编码的完整帧，写协程只需原样写出
	// send holds fully encoded frames that the write goroutine writes out verbatim
	send chan []byte
	// done 关闭后通知读协程与写协程退出
	// done signals both the read goroutine and the write goroutine to exit once closed
	done chan struct{}
	// closeOnce 保证 done 只被关闭一次，服务端 Close 与对端断开两条路径都可能触发关闭，重复 close(done) 会 panic
	// closeOnce makes sure done is closed once; the server Close path and the peer-disconnect path can both trigger it, and a second close(done) panics
	closeOnce sync.Once
}

// close 关闭 done 与底层连接，可重复调用
// close shuts the done channel and the underlying connection, and may be called more than once
func (c *ipcConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// writeLoop 串行消费发送队列并写向 socket，单个慢消费者只阻塞自己的写协程，不影响其他连接与 Publish 调用方
// writeLoop drains the send queue to the socket serially, and a slow consumer blocks only its own write goroutine, not other connections or the Publish caller
//
// 退出时须从 srv.conns 注销自己，否则连接槽位只增不减，累计 8 次连接后新消费者会被拒绝，且已断开的连接仍会被 Publish 计入丢帧
// The goroutine must deregister itself from srv.conns on exit; otherwise slots accumulate, new consumers are refused after 8 connections, and disconnected entries still count as drops in Publish
func (c *ipcConn) writeLoop() {
	defer func() {
		c.close()
		c.srv.removeConn(c)
	}()
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.send:
			if _, err := c.conn.Write(frame); err != nil {
				return
			}
		}
	}
}

// readLoop 探测对端断开，本通道只读，服务端不使用消费者发来的数据
// readLoop detects peer disconnects; the channel is read-only and the server does not use data sent by consumers
//
// 若不读取，对端关闭时的 FIN 与 RST 不会被观察到，写协程会一直阻塞在空队列的 select 上，连接槽位随之泄漏，因此这里持续读取并丢弃，读到错误即视为断开
// Without a reader the FIN and RST from a closing peer go unobserved, the write goroutine stays blocked on an empty select and the slot leaks, so this loop keeps reading and discarding, treating a read error as a disconnect
func (c *ipcConn) readLoop() {
	buf := make([]byte, 256)
	for {
		if _, err := c.conn.Read(buf); err != nil {
			c.close()
			return
		}
		// 消费者发来的数据按协议忽略
		// Data sent by a consumer is ignored per the protocol
	}
}

// removeConn 把连接从活跃集合中移除，可重复调用，由写协程退出时调用以避免槽位泄漏
// removeConn drops a connection from the active set, may be called more than once, and is called from the write goroutine on exit so slots are released
func (s *IPCServer) removeConn(c *ipcConn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// IPCServer 是 TCP 数据通道的服务端
// IPCServer is the server side of the TCP data channel
type IPCServer struct {
	ln    net.Listener
	addr  string
	// hello 预先编码好的握手帧，连接建立时最先入队
	// hello is the pre-encoded handshake frame queued first when a connection is established
	hello []byte

	mu     sync.RWMutex
	conns  map[*ipcConn]struct{}
	closed bool

	dropped atomic.Uint64

	closeOnce sync.Once
	closeErr  error
}

// ipcAddrError 标记地址本身不合法这一类错误，与端口被占用这类环境错误区分开，便于调用方给出准确提示
// ipcAddrError marks an invalid-address error, kept distinct from environment errors such as a port in use so the caller can report accurately
type ipcAddrError struct {
	msg string
}

func (e *ipcAddrError) Error() string { return e.msg }

// newIPCAddrError 构造一个地址类错误
// newIPCAddrError builds an address error
func newIPCAddrError(format string, args ...any) error {
	return &ipcAddrError{msg: fmt.Sprintf(format, args...)}
}

// isIPCAddrError 判断错误是否属于地址不合法这一类
// isIPCAddrError reports whether an error belongs to the invalid-address class
func isIPCAddrError(err error) bool {
	var target *ipcAddrError
	return errors.As(err, &target)
}

// validateIPCAddr 校验监听地址的语法是否可用，不限制必须为回环地址
// validateIPCAddr checks that the listen address is syntactically usable and does not require it to be a loopback address
//
// 监听地址由使用者显式配置，绑定某个网卡或所有网卡是其决定，由于本接口无鉴权，非回环绑定会在 NewIPCServer 中记录安全警告而不是拒绝启动
// The listen address is the user's explicit choice and binding one or all interfaces is their decision; because the interface has no authentication, a non-loopback bind logs a warning in NewIPCServer rather than refusing to start
func validateIPCAddr(addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return newIPCAddrError("invalid listen address %q: %v", addr, err)
	}
	return nil
}

// isLoopbackBind 判断监听地址是否只对本机可见
// isLoopbackBind reports whether the listen address is visible only on this machine
//
// 空 host 如 ":9003" 等价于监听所有接口因此不算回环，无法解析成 IP 的主机名按非回环处理，以避免漏报暴露风险
// An empty host such as ":9003" means all interfaces and therefore is not loopback, and a host name that does not parse as an IP is treated as non-loopback so exposure is not under-reported
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// NewIPCServer 在 addr 上启动监听，端口可以为 0 由系统分配，实际地址由 Addr 返回
// NewIPCServer starts listening on addr, the port may be 0 for the OS to choose, and Addr reports the resulting address
//
// addr 由使用者显式配置，可以是回环地址、某个网卡地址或 ":port"，绑定非回环地址时本接口无鉴权，会记录一条安全警告
// The address is the user's explicit choice and may be a loopback address, an interface address or ":port"; since the interface has no authentication, a non-loopback bind logs a warning
//
// addr 为空表示不启用，返回 (nil, nil)，调用方据此跳过后续操作
// An empty addr disables the feature and returns (nil, nil) so the caller skips the remaining work
func NewIPCServer(addr, version string) (*IPCServer, error) {
	if addr == "" {
		return nil, nil
	}
	if err := validateIPCAddr(addr); err != nil {
		return nil, err
	}

	// 绑定非回环地址意味着无鉴权的 MIDI 流对网络可见，这是使用者显式配置的结果，允许这么做但要有记录
	// A non-loopback bind exposes an unauthenticated MIDI stream to the network; this is the user's explicit choice, so it is allowed and logged
	loopbackOnly := isLoopbackBind(addr)
	if !loopbackOnly {
		golog.Warn("IPC listening address " + addr + " is not loopback: " +
			"the MIDI stream will be reachable from the network and is NOT authenticated")
	}

	helloBody, err := json.Marshal(ipcHello{
		Type:    "hello",
		Proto:   ipcProtoVersion,
		Client:  ipcClientName,
		Version: version,
	})
	if err != nil {
		return nil, fmt.Errorf("encode hello frame: %w", err)
	}
	helloFrame, err := encodeFrame(ipcKindControlJSON, helloBody)
	if err != nil {
		return nil, fmt.Errorf("encode hello frame: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	s := &IPCServer{
		ln:    ln,
		addr:  ln.Addr().String(),
		hello: helloFrame,
		conns: make(map[*ipcConn]struct{}),
	}

	go s.acceptLoop()

	scope := "loopback only"
	if !loopbackOnly {
		scope = "reachable from network, NOT authenticated"
	}
	golog.Info("IPC listening on " + s.addr + " (" + scope + ", read-only)")
	return s, nil
}

// Addr 返回实际绑定地址，包含系统分配的端口，服务端未启用时返回空串
// Addr reports the bound address including an OS-assigned port, and returns an empty string when the server is disabled
func (s *IPCServer) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

// Dropped 返回因消费者队列满而丢弃的帧数累计值
// Dropped reports the running total of frames dropped because a consumer queue was full
func (s *IPCServer) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// acceptLoop 接受新连接直到监听器关闭
// acceptLoop accepts new connections until the listener is closed
func (s *IPCServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// Close 触发的关闭属正常路径，不记错误
			// A close triggered by Close is a normal path and is not logged as an error
			s.mu.RLock()
			closed := s.closed
			s.mu.RUnlock()
			if !closed {
				golog.Error("IPC accept failed: " + err.Error())
			}
			return
		}
		if c := s.addConn(conn); c == nil {
			// addConn 已负责关闭被拒绝的连接
			// addConn already closed the rejected connection
			continue
		}
	}
}

// addConn 注册一条连接并启动读协程与写协程，握手帧排在该连接的最前面
// addConn registers a connection and starts its read and write goroutines, with the handshake frame queued first
//
// 返回 nil 表示连接被拒绝，服务端已关闭或已达连接上限，此时连接已被关闭
// A nil return means the connection was refused because the server is closed or the connection limit was reached, and the connection has been closed
func (s *IPCServer) addConn(conn net.Conn) *ipcConn {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return nil
	}
	if len(s.conns) >= ipcMaxConnections {
		s.mu.Unlock()
		conn.Close()
		golog.Warn("IPC connection rejected: limit of " +
			fmt.Sprint(ipcMaxConnections) + " consumers reached")
		return nil
	}

	c := &ipcConn{
		srv:  s,
		conn: conn,
		send: make(chan []byte, ipcSendQueueSize),
		done: make(chan struct{}),
	}
	// 先入队握手帧再注册，保证它是该连接上的第一帧，并且此时连接还不在 map 中，不会与 Publish 竞争
	// The handshake frame is queued before registration so it is the first frame on the connection, and the connection is not yet in the map so Publish cannot race with it
	c.send <- s.hello
	s.conns[c] = struct{}{}
	s.mu.Unlock()

	go c.writeLoop()
	// 读协程负责探测对端断开，不读取时 FIN 与 RST 不会被观察到，写协程会阻塞在空队列的 select 上，槽位随之泄漏
	// The read goroutine detects peer disconnects; without a reader the FIN and RST go unobserved, the write goroutine stays blocked on an empty select and the slot leaks
	go c.readLoop()
	golog.Info("IPC consumer connected: " + conn.RemoteAddr().String())
	return c
}

// Publish 把一条 MIDI 消息广播给所有已连接的消费者
// Publish broadcasts one MIDI message to every connected consumer
//
// 热路径约束为不阻塞，没有消费者时直接返回且不分配内存，某消费者队列已满时丢弃该帧并累加 Dropped，日志按 ipcDropLogInterval 限速
// The hot path must not block: with no consumers it returns before allocating, a full consumer queue drops the frame and increments Dropped, and the log is rate limited by ipcDropLogInterval
//
// 所有连接共享同一个已编码帧切片，帧入队后不再被修改，因此可以安全并发写出并省去每连接一次拷贝
// All connections share one encoded frame slice that is not modified after enqueue, which makes concurrent writes safe and avoids a copy per connection
func (s *IPCServer) Publish(msg []byte) {
	if s == nil || len(msg) == 0 {
		return
	}

	s.mu.RLock()
	// 无消费者时直接返回，避免热路径上的编码分配
	// Return early with no consumers so the hot path does not allocate for encoding
	if s.closed || len(s.conns) == 0 {
		s.mu.RUnlock()
		return
	}

	frame, err := encodeFrame(ipcKindMIDI, msg)
	if err != nil {
		s.mu.RUnlock()
		golog.Warn("IPC frame dropped: " + err.Error())
		return
	}

	for c := range s.conns {
		select {
		case c.send <- frame:
		default:
			if n := s.dropped.Add(1); n%ipcDropLogInterval == 1 {
				golog.Warn("IPC consumer is slow, dropped " + fmt.Sprint(n) + " frame(s) so far")
			}
		}
	}
	s.mu.RUnlock()
}

// Close 停止监听并断开所有消费者，可重复调用
// Close stops the listener and disconnects every consumer, and may be called more than once
func (s *IPCServer) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		conns := make([]*ipcConn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.conns = make(map[*ipcConn]struct{})
		s.mu.Unlock()

		s.closeErr = s.ln.Close()
		for _, c := range conns {
			// c.close 幂等，写协程可能已因对端断开而关闭过该连接
			// c.close is idempotent and the write goroutine may already have closed this connection after a peer disconnect
			c.close()
		}
		if len(conns) > 0 {
			golog.Info("IPC server closed, disconnected " +
				fmt.Sprint(len(conns)) + " consumer(s)")
		} else {
			golog.Info("IPC server closed")
		}
	})
	return s.closeErr
}

// encodeFrame 把 kind 与 body 编码成带长度前缀的完整帧
// encodeFrame encodes a kind and body into a complete length-prefixed frame
func encodeFrame(kind byte, body []byte) ([]byte, error) {
	total := 1 + len(body)
	if total > ipcMaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d bytes (max %d)", total, ipcMaxFrameSize)
	}
	frame := make([]byte, 4+total)
	binary.LittleEndian.PutUint32(frame[0:4], uint32(total))
	frame[4] = kind
	copy(frame[5:], body)
	return frame, nil
}

// writeFrame 编码并写出单个帧
// writeFrame encodes and writes a single frame
func writeFrame(w io.Writer, kind byte, body []byte) error {
	frame, err := encodeFrame(kind, body)
	if err != nil {
		return err
	}
	_, err = w.Write(frame)
	return err
}

// readFrame 从 r 读取一个完整帧并返回其 kind 与 body
// readFrame reads one complete frame from r and returns its kind and body
//
// 对端可能分多次写出，因此用 io.ReadFull 读满所需字节，length 为 0 或超过 ipcMaxFrameSize 时拒绝，避免畸形数据触发过大的内存分配
// A peer may write in several parts, so io.ReadFull fills the promised bytes; a length of zero or above ipcMaxFrameSize is refused so malformed data cannot trigger a large allocation
func readFrame(r io.Reader) (kind byte, body []byte, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	length := binary.LittleEndian.Uint32(hdr[:])
	if length == 0 {
		return 0, nil, fmt.Errorf("invalid frame: zero length")
	}
	if length > ipcMaxFrameSize {
		return 0, nil, fmt.Errorf("invalid frame: length %d exceeds max %d", length, ipcMaxFrameSize)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}
