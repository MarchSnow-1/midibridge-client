package main

import (
	golog "github.com/donnie4w/go-logger/logger"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type clientState int

const (
	stateIdle clientState = iota
	stateConnecting
	stateAuthenticating
	stateConnected
	stateReconnecting
	stateFailed
)

func (s clientState) String() string {
	return [...]string{"IDLE", "CONNECTING", "AUTHENTICATING", "CONNECTED", "RECONNECTING", "FAILED"}[s]
}

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
	Data    []byte
	DeltaMs float64
}

type WSClient struct {
	host     string
	port     int
	password string
	rc       ReconnectConfig

	state          clientState
	conn           *websocket.Conn
	reconnectCount int

	MidiChan   chan MidiEvent
	StatusChan chan StatusEvent

	stopChan chan struct{}
	readDone chan string
	mu       sync.Mutex
}

func NewWSClient() *WSClient {
	return &WSClient{
		MidiChan:   make(chan MidiEvent, 256),
		StatusChan: make(chan StatusEvent, 32),
		stopChan:   make(chan struct{}),
		readDone:   make(chan string, 1),
	}
}

func (w *WSClient) Connect(host string, port int, password string, rc ReconnectConfig) {
	w.host = host
	w.port = port
	w.password = password
	w.rc = rc
	w.state = stateIdle
	go w.connectLoop()
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

func (w *WSClient) setState(s clientState) {
	w.state = s
}

func (w *WSClient) connectLoop() {
	firstDial := true
	for {
		select {
		case <-w.stopChan:
			return
		default:
		}

		if w.rc.MaxAttempts > 0 && w.reconnectCount >= w.rc.MaxAttempts {
			w.setState(stateFailed)
			w.emitStatus(StatusEvent{Type: "max_reconnects"})
			return
		}

		url := fmt.Sprintf("ws://%s:%d", w.host, w.port)
		if firstDial {
			w.setState(stateConnecting)
			golog.Info(T("wsClient.connecting", map[string]string{"url": url}))
		} else {
			w.setState(stateConnecting)
		}

		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			golog.Error(T("wsClient.connectingFailed", map[string]string{"url": url, "error": err.Error()}))
			if !w.rc.Enabled {
				w.setState(stateFailed)
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

		firstDial = false
		w.reconnectCount = 0
		w.setState(stateAuthenticating)
		w.emitStatus(StatusEvent{Type: "connected"})

		authMsg, _ := json.Marshal(map[string]string{"type": "auth", "password": w.password})
		if err := conn.WriteMessage(websocket.TextMessage, authMsg); err != nil {
			golog.Error(T("wsClient.error", map[string]string{"error": err.Error()}))
			w.closeConn()
			if !w.rc.Enabled {
				w.setState(stateFailed)
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

		if reason == "failed" {
			w.setState(stateFailed)
			return
		}

		if !w.rc.Enabled {
			w.setState(stateFailed)
			return
		}

		w.reconnectWait()
		if w.isStopped() {
			return
		}
	}
}

func (w *WSClient) readPump() {
	var reason string
	defer func() {
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

func (w *WSClient) handleMessage(raw []byte) string {
	var msg serverMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		golog.Warn(T("wsClient.unparseable", nil))
		return ""
	}

	switch msg.Type {
	case "auth_ok":
		w.setState(stateConnected)
		w.emitStatus(StatusEvent{Type: "authenticated"})

	case "auth_fail":
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
			select {
			case w.MidiChan <- MidiEvent{Data: data.Bytes, DeltaMs: data.Time}:
			default:
			}
		}

	case "kicked":
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
	w.reconnectCount++
	w.setState(stateReconnecting)
	delay := w.reconnectDelay()
	delaySec := fmt.Sprintf("%.1f", delay.Seconds())
	golog.Info(T("wsClient.reconnecting", map[string]string{"delay": delaySec, "attempt": itoa(w.reconnectCount)}))
	w.emitStatus(StatusEvent{Type: "reconnecting", Reason: delaySec})

	select {
	case <-w.stopChan:
	case <-time.After(delay):
	}
}

func (w *WSClient) reconnectDelay() time.Duration {
	baseDelay := float64(w.rc.IntervalMs) * math.Pow(1.5, float64(w.reconnectCount-1))
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
