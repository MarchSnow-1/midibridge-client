// 本文件覆盖 ipc.go 的帧编解码、地址校验与连接投递行为，断言分布在协议层与连接层两处
// 协议层用例不依赖真实 socket，通过 bytes.Buffer 与 net.Pipe 构造确定性输入
// 连接层用例使用 127.0.0.1 与端口 0，避免占用固定端口并可在任意平台运行
//
// This file covers the frame codec, address validation and delivery behaviour of ipc.go, with assertions at the protocol layer and the connection layer
// Protocol cases avoid real sockets by driving deterministic input through bytes.Buffer and net.Pipe
// Connection cases bind 127.0.0.1 with port 0 so no fixed port is taken and the tests run on any platform
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// ===== 帧编解码 / Frame codec =====

func TestIPCFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		kind byte
		body []byte
	}{
		{"midi note on", ipcKindMIDI, []byte{0x90, 60, 100}},
		{"midi note off", ipcKindMIDI, []byte{0x80, 60, 0}},
		{"empty midi body", ipcKindMIDI, nil},
		{"control json", ipcKindControlJSON, []byte(`{"type":"hello"}`)},
		{"single byte", ipcKindMIDI, []byte{0xF8}},
		{"sysex", ipcKindMIDI, append([]byte{0xF0, 0x7E, 0x7F}, 0xF7)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, c.kind, c.body); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}

			// 长度字段必须等于 kind + body
			raw := buf.Bytes()
			if len(raw) < 4 {
				t.Fatalf("frame too short: %d bytes", len(raw))
			}
			gotLen := binary.LittleEndian.Uint32(raw[:4])
			if want := uint32(1 + len(c.body)); gotLen != want {
				t.Fatalf("length field = %d, want %d", gotLen, want)
			}

			kind, body, err := readFrame(&buf)
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}
			if kind != c.kind {
				t.Fatalf("kind = %#x, want %#x", kind, c.kind)
			}
			if !bytes.Equal(body, c.body) {
				t.Fatalf("body = % X, want % X", body, c.body)
			}
			if buf.Len() != 0 {
				t.Fatalf("%d trailing bytes after frame", buf.Len())
			}
		})
	}
}

func TestIPCFrameRejectsOversizedBody(t *testing.T) {
	body := make([]byte, ipcMaxFrameSize) // 加 1 字节 kind 后超限
	var buf bytes.Buffer
	if err := writeFrame(&buf, ipcKindMIDI, body); err == nil {
		t.Fatal("writeFrame accepted a body larger than the frame limit")
	}
	if buf.Len() != 0 {
		t.Fatalf("writeFrame wrote %d bytes before failing", buf.Len())
	}
}

func TestIPCFrameAcceptsMaximumSize(t *testing.T) {
	// 恰好达到上限必须被接受（边界值）
	body := make([]byte, ipcMaxFrameSize-1)
	var buf bytes.Buffer
	if err := writeFrame(&buf, ipcKindMIDI, body); err != nil {
		t.Fatalf("writeFrame at exact limit: %v", err)
	}
	if _, _, err := readFrame(&buf); err != nil {
		t.Fatalf("readFrame at exact limit: %v", err)
	}
}

func TestReadFrameRejectsInvalidLength(t *testing.T) {
	t.Run("zero length", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint32(0))
		if _, _, err := readFrame(&buf); err == nil {
			t.Fatal("readFrame accepted a zero-length frame")
		}
	})

	t.Run("over limit", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint32(ipcMaxFrameSize+1))
		if _, _, err := readFrame(&buf); err == nil {
			t.Fatal("readFrame accepted a frame above the size limit")
		}
	})

	t.Run("truncated header", func(t *testing.T) {
		buf := bytes.NewBuffer([]byte{0x01, 0x02})
		if _, _, err := readFrame(buf); err == nil {
			t.Fatal("readFrame accepted a truncated header")
		}
	})

	t.Run("truncated body", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint32(4))
		buf.Write([]byte{ipcKindMIDI, 0x90}) // 声称 4 字节但只给 2 字节
		if _, _, err := readFrame(&buf); err == nil {
			t.Fatal("readFrame accepted a truncated body")
		}
	})
}

// TestIPCFrameReadHandlesSplitWrites 验证读取端能处理"一次读不到完整帧"的情况
// 这是所有语言消费者的必备能力：TCP 不保证消息边界
func TestIPCFrameReadHandlesSplitWrites(t *testing.T) {
	full, err := encodeFrame(ipcKindMIDI, []byte{0x90, 60, 100})
	if err != nil {
		t.Fatalf("encodeFrame: %v", err)
	}

	pr, pw := io.Pipe()
	go func() {
		// 逐字节写出，最大化分片
		for _, b := range full {
			pw.Write([]byte{b})
		}
		pw.Close()
	}()

	kind, body, err := readFrame(pr)
	if err != nil {
		t.Fatalf("readFrame over drip-fed stream: %v", err)
	}
	if kind != ipcKindMIDI {
		t.Fatalf("kind = %#x, want %#x", kind, ipcKindMIDI)
	}
	if !bytes.Equal(body, []byte{0x90, 60, 100}) {
		t.Fatalf("body = % X", body)
	}
}

// ===== 地址校验 =====

// TestIPCAcceptsNonLoopbackAddresses 确认监听地址由用户自由指定：
// 绑定到 0.0.0.0 / 具体网卡是合法用法，只是会被标记为非回环（并触发安全警告）
//
// 这里用端口 0 避免占用固定端口；若主机没有对应网卡（如公网 IP），
// 监听本身会失败，那属于环境限制而非地址被拒绝，因此单独处理
func TestIPCAcceptsNonLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0"} {
		t.Run(addr, func(t *testing.T) {
			s, err := NewIPCServer(addr, "test")
			if err != nil {
				// 某些平台可能不允许绑定全接口（例如受限网络环境），
				// 那属于环境限制，不算地址校验失败
				t.Skipf("environment refused %q: %v", addr, err)
			}
			defer s.Close()

			if s.Addr() == "" {
				t.Fatal("Addr() is empty")
			}
			// 非回环绑定必须被识别出来，以便记录安全警告
			if isLoopbackBind(addr) {
				t.Fatalf("isLoopbackBind(%q) = true, want false", addr)
			}
		})
	}
}

// TestIPCRejectsMalformedAddresses 确认语法错误的地址仍会报错
func TestIPCRejectsMalformedAddresses(t *testing.T) {
	cases := []string{
		"127.0.0.1",  // 缺少端口
		"not-an-addr", // 既无 host 也无端口
	}

	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			s, err := NewIPCServer(addr, "test")
			if err == nil {
				if s != nil {
					s.Close()
				}
				t.Fatalf("NewIPCServer accepted malformed address %q", addr)
			}
			if !isIPCAddrError(err) {
				t.Fatalf("error is not an address error: %v", err)
			}
		})
	}
}

// TestIPCIsLoopbackBind 覆盖回环判定的各类输入
func TestIPCIsLoopbackBind(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9003", true},
		{"localhost:9003", true},
		{"[::1]:9003", true},
		{"0.0.0.0:9003", false},
		{":9003", false},
		{"[::]:9003", false},
		{"192.168.1.10:9003", false},
		{"example.com:9003", false},
	}
	for _, c := range cases {
		if got := isLoopbackBind(c.addr); got != c.want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestIPCAcceptsLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		t.Run(addr, func(t *testing.T) {
			s, err := NewIPCServer(addr, "test")
			if err != nil {
				t.Fatalf("NewIPCServer(%q): %v", addr, err)
			}
			defer s.Close()
			if s.Addr() == "" {
				t.Fatal("Addr() is empty")
			}
			// 端口 0 必须被替换为真实端口
			_, port, err := net.SplitHostPort(s.Addr())
			if err != nil {
				t.Fatalf("SplitHostPort(%q): %v", s.Addr(), err)
			}
			if port == "0" || port == "" {
				t.Fatalf("Addr() = %q, port was not assigned", s.Addr())
			}
		})
	}
}

func TestIPCEmptyAddrDisablesServer(t *testing.T) {
	s, err := NewIPCServer("", "test")
	if err != nil {
		t.Fatalf("NewIPCServer(\"\"): %v", err)
	}
	if s != nil {
		s.Close()
		t.Fatal("empty address should disable the server, got a non-nil server")
	}

	// nil 服务端上的所有调用都必须是安全的空操作
	if got := s.Addr(); got != "" {
		t.Fatalf("nil Addr() = %q, want empty", got)
	}
	if got := s.Dropped(); got != 0 {
		t.Fatalf("nil Dropped() = %d, want 0", got)
	}
	s.Publish([]byte{0x90, 60, 100})
	if err := s.Close(); err != nil {
		t.Fatalf("nil Close() = %v, want nil", err)
	}
}

// ===== 端到端行为 =====

// dialIPC 连接服务端并返回已读掉握手帧之后的连接
func dialIPC(t *testing.T, s *IPCServer) (net.Conn, ipcHello) {
	t.Helper()
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial %s: %v", s.Addr(), err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	kind, body, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if kind != ipcKindControlJSON {
		t.Fatalf("first frame kind = %#x, want control JSON %#x", kind, ipcKindControlJSON)
	}
	var hello ipcHello
	if err := json.Unmarshal(body, &hello); err != nil {
		t.Fatalf("decode hello %q: %v", body, err)
	}
	return conn, hello
}

func TestIPCHelloFrameIsFirst(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "v1.2.3-test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	_, hello := dialIPC(t, s)

	if hello.Type != "hello" {
		t.Fatalf("hello.Type = %q, want \"hello\"", hello.Type)
	}
	if hello.Proto != IPCOProtoVersion {
		t.Fatalf("hello.Proto = %q, want %q", hello.Proto, IPCOProtoVersion)
	}
	if hello.Client != ipcClientName {
		t.Fatalf("hello.Client = %q, want %q", hello.Client, ipcClientName)
	}
	if hello.Version != "v1.2.3-test" {
		t.Fatalf("hello.Version = %q, want \"v1.2.3-test\"", hello.Version)
	}
}

func TestIPCPublishDeliversToConsumer(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conn, _ := dialIPC(t, s)

	want := [][]byte{
		{0x90, 60, 100},
		{0x80, 60, 0},
		{0xB0, 7, 64},
		{0xF8},
	}
	for _, m := range want {
		s.Publish(m)
	}

	for i, w := range want {
		kind, body, err := readFrame(conn)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if kind != ipcKindMIDI {
			t.Fatalf("frame %d kind = %#x, want MIDI %#x", i, kind, ipcKindMIDI)
		}
		if !bytes.Equal(body, w) {
			t.Fatalf("frame %d body = % X, want % X", i, body, w)
		}
	}

	if d := s.Dropped(); d != 0 {
		t.Fatalf("Dropped() = %d, want 0 when the consumer keeps up", d)
	}
}

func TestIPCPublishBroadcastsToMultipleConsumers(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	const consumers = 3
	conns := make([]net.Conn, 0, consumers)
	for i := 0; i < consumers; i++ {
		c, _ := dialIPC(t, s)
		conns = append(conns, c)
	}

	want := []byte{0x90, 64, 127}
	s.Publish(want)

	for i, c := range conns {
		kind, body, err := readFrame(c)
		if err != nil {
			t.Fatalf("consumer %d read: %v", i, err)
		}
		if kind != ipcKindMIDI || !bytes.Equal(body, want) {
			t.Fatalf("consumer %d got kind=%#x body=% X", i, kind, body)
		}
	}
}

func TestIPCPublishWithoutConsumersIsNoop(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	// 无消费者时既不能 panic，也不能计入丢弃
	for i := 0; i < 100; i++ {
		s.Publish([]byte{0x90, byte(i), 100})
	}
	if d := s.Dropped(); d != 0 {
		t.Fatalf("Dropped() = %d, want 0 with no consumers", d)
	}
}

// TestIPCSlowConsumerDropsFrames 验证慢消费者不会阻塞 Publish，
// 且丢弃会被计数。消费者连上后完全不读取，写协程阻塞在 socket 写、
// 发送队列被填满后新帧只能被丢弃
func TestIPCSlowConsumerDropsFrames(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	// 连上但不读 —— 注意不调用 dialIPC 之后也不要读 hello
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 等到服务端登记该连接，否则 Publish 会走"无消费者"快路径
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumer was not registered in time")
		}
		time.Sleep(time.Millisecond)
	}

	// 持续投递直到出现丢弃：socket 缓冲区 + 发送队列的容量有限，
	// 消费者不消费时必然会溢出。上限设得足够大以免误判
	const maxPublish = 500000
	msg := []byte{0x90, 60, 100}
	var published int
	for published = 0; published < maxPublish; published++ {
		s.Publish(msg)
		if published%1000 == 0 && s.Dropped() > 0 {
			break
		}
	}

	if s.Dropped() == 0 {
		t.Fatalf("no frames were dropped after %d publishes to a stalled consumer", published)
	}
}

// TestIPCPublishDoesNotBlockOnStalledConsumer 保证 Publish 在消费者完全
// 卡死时依然即时返回 —— 这是 MIDI 热路径的硬性要求
func TestIPCPublishDoesNotBlockOnStalledConsumer(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumer was not registered in time")
		}
		time.Sleep(time.Millisecond)
	}

	// 先把管道压满
	msg := []byte{0x90, 60, 100}
	for i := 0; i < 100000 && s.Dropped() == 0; i++ {
		s.Publish(msg)
	}

	// 此时再投递必须仍然立即返回。给一个宽松上限：即使调度抖动，
	// 1000 次投递也不应接近秒级
	start := time.Now()
	for i := 0; i < 1000; i++ {
		s.Publish(msg)
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("Publish blocked on a stalled consumer: 1000 calls took %v", elapsed)
	}
}

func TestIPCEstablishedConsumerStopsReceivingAfterClose(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	conn, _ := dialIPC(t, s)
	s.Publish([]byte{0x90, 60, 100})
	if _, _, err := readFrame(conn); err != nil {
		t.Fatalf("read before close: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 连接应被服务端关闭：读取要么遇到 EOF，要么超时但没有新数据
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := readFrame(conn); err == nil {
		t.Fatal("consumer still received a frame after Close")
	}
}

func TestIPCCloseIsIdempotentAndPublishSafeAfterwards(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	conn, _ := dialIPC(t, s)

	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Close 之后 Publish 必须是安全的空操作（不得 panic、不得阻塞）
	for i := 0; i < 100; i++ {
		s.Publish([]byte{0x90, 60, 100})
	}

	// 监听器已关闭，新连接必须被拒绝
	if c, err := net.Dial("tcp", s.Addr()); err == nil {
		c.Close()
		t.Fatal("dial succeeded after Close")
	}
	conn.Close()
}

func TestIPCRejectsBeyondConnectionLimit(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	// 建立允许数量的连接
	for i := 0; i < ipcMaxConnections; i++ {
		dialIPC(t, s)
	}

	// 再多一条应被立即关闭：读取时得到 EOF 而不是握手帧
	extra, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial %d: %v", ipcMaxConnections+1, err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := readFrame(extra); err == nil {
		t.Fatal("server accepted a connection beyond the limit")
	}

	// 已有连接不受影响
	s.Publish([]byte{0x90, 60, 100})
	if d := s.Dropped(); d != 0 {
		t.Fatalf("Dropped() = %d, want 0 (existing consumers keep up)", d)
	}
}

// TestIPCPublishSkipsEmptyMessage 保证空消息不会被编码成非法帧
func TestIPCPublishSkipsEmptyMessage(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conn, _ := dialIPC(t, s)

	s.Publish(nil)
	s.Publish([]byte{})

	// 紧随其后发一条真实消息，确认它没有被空消息干扰
	want := []byte{0x90, 60, 100}
	s.Publish(want)
	_, body, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("body = % X, want % X", body, want)
	}
}
