package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	golog "github.com/donnie4w/go-logger/logger"
	"github.com/gorilla/websocket"
)

const (
	// wsPingInterval 客户端主动发送协议层 ping 的间隔（保活）。
	wsPingInterval = 30 * time.Second
	// wsReadTimeout 读超时：超过该时间未收到任何帧（含 pong）即判定连接已死。
	wsReadTimeout = 60 * time.Second
	// authWatchdogTimeout 发出认证后的等待上限：超时未收到认证结果即断开重连，
	// 防止服务器接受连接后不应答导致客户端永久"假在线"。
	authWatchdogTimeout = 5 * time.Second
	// maxReadMessageSize 单帧读取上限（覆盖大型 SysEx），防止超大帧耗尽内存。
	maxReadMessageSize = 1 << 20
	// minReconnectIntervalMs 重连间隔的下限钳制：防止配置为 0/负数后
	// 退化为无退避的高频重连风暴。
	minReconnectIntervalMs = 250
)

type StatusEvent struct {
	Type   string
	Code   int
	Reason string
}

type serverMsg struct {
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

type midiData struct {
	Time  float64 `json:"t"`
	Bytes []byte  `json:"m"`
}

type MidiEvent struct {
	Data []byte
	// DeltaSec 距上一条消息的时间增量（秒）。
	// 协议字段 "t" 即秒；旧名 DeltaMs 是误导性命名。
	DeltaSec float64
}

type WSClient struct {
	host     string
	port     int
	password string
	rc       ReconnectConfig
	tlsCfg   TLSConfig

	conn           *websocket.Conn
	reconnectCount int

	MidiChan   chan MidiEvent
	StatusChan chan StatusEvent

	stopChan chan struct{}
	readDone chan string
	mu       sync.Mutex

	authWatchdog *time.Timer // 认证超时看门狗（mu 保护）
}

func NewWSClient() *WSClient {
	return &WSClient{
		MidiChan:   make(chan MidiEvent, 256),
		StatusChan: make(chan StatusEvent, 32),
		stopChan:   make(chan struct{}),
		readDone:   make(chan string, 1),
	}
}

func (w *WSClient) Connect(host string, port int, password string, rc ReconnectConfig, tlsCfg TLSConfig) {
	w.host = host
	w.port = port
	w.password = password
	w.rc = rc
	w.tlsCfg = tlsCfg
	go w.connectLoop()
}

// buildDialer 构造 WebSocket 拨号器。
// TLS 启用时使用 wss:// 并校验服务端证书；配置了 CACert 时额外信任该
// 证书（自签场景）。握手超时防止恶意/无响应对端长期挂起拨号。
func (w *WSClient) buildDialer() (*websocket.Dialer, error) {
	d := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}
	if w.tlsCfg.Enabled {
		tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
		if w.tlsCfg.CACert != "" {
			pem, err := os.ReadFile(w.tlsCfg.CACert)
			if err != nil {
				return nil, fmt.Errorf("failed to read tls.caCert %s: %w", w.tlsCfg.CACert, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("tls.caCert %s contains no valid PEM certificate", w.tlsCfg.CACert)
			}
			tlsConf.RootCAs = pool
		}
		d.TLSClientConfig = tlsConf
	}
	return d, nil
}

// dialURL 按 TLS 配置返回 ws:// 或 wss:// 连接地址。
func (w *WSClient) dialURL() string {
	scheme := "ws"
	if w.tlsCfg.Enabled {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, w.host, w.port)
}

func (w *WSClient) Disconnect() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stopChan:
	default:
		close(w.stopChan)
	}
	if w.conn != nil {
		w.conn.Close()
	}
}

func (w *WSClient) connectLoop() {
	firstDial := true
	for {
		select {
		case <-w.stopChan:
			return
		default:
		}

		w.mu.Lock()
		count := w.reconnectCount
		w.mu.Unlock()
		if w.rc.MaxAttempts > 0 && count >= w.rc.MaxAttempts {
			w.emitStatus(StatusEvent{Type: "max_reconnects"})
			return
		}

		url := w.dialURL()
		if firstDial {
			golog.Info(T("wsClient.connecting", map[string]string{"url": url}))
		}

		dialer, err := w.buildDialer()
		if err != nil {
			golog.Error(T("wsClient.connectingFailed", map[string]string{"url": url, "error": err.Error()}))
			if !w.rc.Enabled {
				return
			}
			w.reconnectWait()
			if w.isStopped() {
				return
			}
			continue
		}

		conn, _, err := dialer.Dial(url, nil)
		if err != nil {
			golog.Error(T("wsClient.connectingFailed", map[string]string{"url": url, "error": err.Error()}))
			if !w.rc.Enabled {
				return
			}
			w.reconnectWait()
			if w.isStopped() {
				return
			}
			continue
		}

		w.mu.Lock()
		w.conn = conn
		w.mu.Unlock()

		// 读取限制与超时：防超大帧耗尽内存；读超时+pong 续期检出半开/死亡连接
		conn.SetReadLimit(maxReadMessageSize)
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
			return nil
		})

		firstDial = false
		w.emitStatus(StatusEvent{Type: "connected"})

		// 认证看门狗：超时未收到认证结果即关闭连接进入重连
		w.armAuthWatchdog(conn)

		authMsg, _ := json.Marshal(map[string]string{"type": "auth", "password": w.password})
		if err := conn.WriteMessage(websocket.TextMessage, authMsg); err != nil {
			golog.Error(T("wsClient.error", map[string]string{"error": err.Error()}))
			w.closeConn()
			if !w.rc.Enabled {
				return
			}
			w.reconnectWait()
			if w.isStopped() {
				return
			}
			continue
		}
		golog.Info(T("wsClient.authSent", nil))

		go w.readPump()

		reason := <-w.readDone
		w.closeConn()

		// 排空旧会话积压事件：带着过时时间增量的旧事件在新连接建立后
		// 迟到重放会造成时序错乱（配对的 Note Off 可能已被丢弃导致卡音）
		w.drainMidiChan()

		if reason == "failed" {
			return
		}

		if !w.rc.Enabled {
			return
		}

		w.reconnectWait()
		if w.isStopped() {
			return
		}
	}
}

// drainMidiChan 非阻塞地丢弃 MidiChan 中积压的旧会话事件。
func (w *WSClient) drainMidiChan() {
	dropped := 0
	for {
		select {
		case <-w.MidiChan:
			dropped++
		default:
			if dropped > 0 {
				golog.Info(fmt.Sprintf("Discarded %d stale MIDI event(s) from previous session", dropped))
			}
			return
		}
	}
}

func (w *WSClient) readPump() {
	var reason string
	// 心跳：周期性发送协议层 ping（WriteControl 按 gorilla 约定可与
	// ReadMessage 并发），配合读超时检出"服务器静默死亡"的半开连接
	pingTicker := time.NewTicker(wsPingInterval)
	pingStop := make(chan struct{})
	go func() {
		for {
			select {
			case <-pingStop:
				return
			case <-w.stopChan:
				return
			case <-pingTicker.C:
				w.mu.Lock()
				conn := w.conn
				w.mu.Unlock()
				if conn == nil {
					return
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					// ping 失败不必额外处理：读超时会把死连接暴露出来
					return
				}
			}
		}
	}()
	defer func() {
		pingTicker.Stop()
		close(pingStop)
		w.readDone <- reason
	}()

	for {
		select {
		case <-w.stopChan:
			reason = "closed"
			return
		default:
		}

		_, raw, err := w.conn.ReadMessage()
		if err != nil {
			select {
			case <-w.stopChan:
				reason = "closed"
			default:
				w.emitStatus(StatusEvent{Type: "disconnected"})
				reason = "closed"
			}
			return
		}

		reason = w.handleMessage(raw)
		if reason != "" {
			return
		}
	}
}

// armAuthWatchdog 启动认证看门狗：超时未收到认证结果即关闭连接，
// 让 readPump 走失败路径触发重连，杜绝"认证中的永久假在线"。
func (w *WSClient) armAuthWatchdog(conn *websocket.Conn) {
	timer := time.AfterFunc(authWatchdogTimeout, func() {
		golog.Warn(T("wsClient.authTimeout", nil))
		conn.Close()
	})
	w.mu.Lock()
	w.authWatchdog = timer
	w.mu.Unlock()
}

// stopAuthWatchdog 停止认证看门狗（收到任何认证结果后调用）。
func (w *WSClient) stopAuthWatchdog() {
	w.mu.Lock()
	if w.authWatchdog != nil {
		w.authWatchdog.Stop()
		w.authWatchdog = nil
	}
	w.mu.Unlock()
}

// validMidiMessage 校验一条（单条）MIDI 消息的结构合法性。
// 上游应为完整消息（服务端按消息转发），此处只做防御性校验：
//   - 首字节必须是状态字节（≥0x80）
//   - 通道消息长度必须与类型匹配
//   - 所有数据字节必须 <0x80（MIDI 规范：数据字节最高位恒为 0）
//   - SysEx 必须以 0xF0 开始、0xF7 结束，且不超过大小上限
//   - 系统实时消息（0xF8-0xFF）恒为 1 字节
const (
	maxSysExLen = 64 * 1024
)

func validMidiMessage(data []byte) bool {
	if len(data) == 0 || len(data) > maxSysExLen {
		return false
	}
	status := data[0]
	if status < 0x80 {
		return false // 首字节必须是状态字节
	}

	msgType := status & 0xF0
	switch {
	case status >= 0xF0:
		switch status {
		case 0xF0: // SysEx：F0 ... F7
			return len(data) >= 2 && data[len(data)-1] == 0xF7 &&
				validDataBytes(data[1:len(data)-1])
		case 0xF1, 0xF3: // MTC Quarter Frame, Song Select — 2 字节
			return len(data) == 2 && validDataBytes(data[1:])
		case 0xF2: // Song Position Pointer — 3 字节
			return len(data) == 3 && validDataBytes(data[1:])
		case 0xF6, 0xF8, 0xFA, 0xFB, 0xFC, 0xFE, 0xFF: // 单字节实时/通用
			return len(data) == 1
		case 0xF4, 0xF5, 0xF7, 0xF9, 0xFD: // 未定义/结束符不应作为首字节
			return false
		}
		return false
	case msgType == 0xC0 || msgType == 0xD0: // Program Change / Channel Pressure — 2 字节
		return len(data) == 2 && validDataBytes(data[1:])
	default: // 其余通道消息（Note/CC/Pitch Bend）— 3 字节
		return len(data) == 3 && validDataBytes(data[1:])
	}
}

// validDataBytes 校验所有数据字节均 <0x80。
// 状态字节以 0x80+ 区分，放行高位置位的数据字节会让畸形消息直达本地 MIDI 驱动。
func validDataBytes(data []byte) bool {
	for _, b := range data {
		if b >= 0x80 {
			return false
		}
	}
	return true
}

func (w *WSClient) handleMessage(raw []byte) string {
	var msg serverMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		golog.Warn(T("wsClient.unparseable", nil))
		return ""
	}

	switch msg.Type {
	case "auth_ok":
		w.stopAuthWatchdog()
		// 仅在真正认证成功后清零重连计数：防止"接受连接即秒断"的
		// 恶意服务器反复重置计数、绕过 MaxAttempts 上限
		w.mu.Lock()
		w.reconnectCount = 0
		w.mu.Unlock()
		w.emitStatus(StatusEvent{Type: "authenticated"})

	case "auth_fail":
		w.stopAuthWatchdog()
		reason := msg.Reason
		if reason == "" {
			reason = "wrong password"
		}
		golog.Error(T("wsClient.authFailed", map[string]string{"reason": reason}))
		w.emitStatus(StatusEvent{Type: "auth_failed", Reason: reason})
		return "failed"

	case "midi":
		var data midiData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			golog.Warn("midi parse error: " + err.Error())
		} else if len(data.Bytes) > 0 {
			// 服务器下发数据校验：防止被入侵/恶意的服务器
			// 向本机 MIDI 设备注入畸形或超大字节流
			if !validMidiMessage(data.Bytes) {
				golog.Warn("Discarded invalid MIDI message from server")
				return ""
			}
			select {
			case w.MidiChan <- MidiEvent{Data: data.Bytes, DeltaSec: data.Time}:
			default:
			}
		}

	case "kicked":
		w.stopAuthWatchdog()
		reason := msg.Reason
		if reason == "" {
			reason = "unknown reason"
		}
		golog.Warn(T("wsClient.kicked", map[string]string{"reason": reason}))
		w.emitStatus(StatusEvent{Type: "kicked", Reason: reason})
		if reason == "password_changed" {
			return "failed"
		}
		return "closed"

	case "pong":
		// No-op

	default:
		golog.Warn(T("wsClient.unknownMessage", map[string]string{"type": msg.Type}))
	}

	return ""
}

func (w *WSClient) reconnectWait() {
	w.mu.Lock()
	w.reconnectCount++
	count := w.reconnectCount
	delay := w.reconnectDelayLocked(count)
	w.mu.Unlock()

	delaySec := fmt.Sprintf("%.1f", delay.Seconds())
	golog.Info(T("wsClient.reconnecting", map[string]string{"delay": delaySec, "attempt": strconv.Itoa(count)}))
	w.emitStatus(StatusEvent{Type: "reconnecting", Reason: delaySec})

	select {
	case <-w.stopChan:
	case <-time.After(delay):
	}
}

// reconnectDelayLocked 计算第 count 次重连的退避延迟。调用方必须已持有 w.mu。
// intervalMs 配置为 0/负数时钳制为下限（minReconnectIntervalMs），
// 防止退化为无退避的高频重连风暴。
func (w *WSClient) reconnectDelayLocked(count int) time.Duration {
	intervalMs := w.rc.IntervalMs
	if intervalMs < minReconnectIntervalMs {
		intervalMs = minReconnectIntervalMs
	}
	baseDelay := float64(intervalMs) * math.Pow(1.5, float64(count-1))
	maxDelay := 30000.0
	jitter := float64(rand.Intn(1000))
	delayMs := math.Min(baseDelay, maxDelay) + jitter
	return time.Duration(delayMs) * time.Millisecond
}

func (w *WSClient) isStopped() bool {
	select {
	case <-w.stopChan:
		return true
	default:
		return false
	}
}

func (w *WSClient) closeConn() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn != nil {
		w.conn.Close()
		w.conn = nil
	}
}

func (w *WSClient) emitStatus(evt StatusEvent) {
	select {
	case w.StatusChan <- evt:
	default:
	}
}
