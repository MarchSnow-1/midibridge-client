package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	golog "github.com/donnie4w/go-logger/logger"
)

var version = "dev"

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Println("midibridge-client", version)
			return
		}
	}

	initLogger(false)

	golog.Info("MIDIBridge Client " + version + " starting...")

	// 配置文件锚定到可执行文件所在目录，避免"从其他目录启动时
	// 读到/生成另一份配置"以及工作目录被诱导劫持配置来源。
	// 无法解析可执行文件路径时回退为当前目录（保持旧行为）。
	configDir := "."
	if exePath, err := os.Executable(); err == nil {
		configDir = filepath.Dir(exePath)
	}
	configPath := filepath.Join(configDir, "data", "config.json")
	cfg, err := loadConfig(configPath)
	if err != nil {
		golog.Error("Startup failed: " + err.Error())
		os.Exit(1)
	}

	initI18N(cfg.Lang)

	// 启动日志中的目标 URL 按实际协议显示：TLS 启用时为 wss://，
	// 否则为 ws://（与 wsclient.dialURL 保持一致）。
	scheme := "ws"
	if cfg.TLS.Enabled {
		scheme = "wss"
	}
	golog.Info(T("index.targetServer", map[string]string{"scheme": scheme, "host": cfg.Server.Host, "port": strconv.Itoa(cfg.Server.Port)}))

	if cfg.Logging.File {
		enableFileLogging()
	}

	midiOut := NewMidiOutput()
	if err := midiOut.Init(cfg.MIDI.VirtualPortName); err != nil {
		golog.Error("MIDI init failed: " + err.Error())
		os.Exit(1)
	}

	wsClient := NewWSClient()

	verboseLog := cfg.Logging.MidiVerbose

	go func() {
		var lastSend time.Time
		for evt := range wsClient.MidiChan {
			data := evt.Data

			// 跟踪按下的音符（按通道+音符复合键，支持多通道流）
			if len(data) >= 3 {
				channel := data[0] & 0x0F
				switch data[0] & 0xF0 {
				case 0x90:
					if data[2] > 0 {
						midiOut.HoldNote(channel, data[1])
					} else {
						midiOut.ReleaseNote(channel, data[1])
					}
				case 0x80:
					midiOut.ReleaseNote(channel, data[1])
				}
			}

			if verboseLog {
				if s := midiVerbose(data); s != "" {
					golog.Info(s)
				}
			}

			// 按 delta 时间控制发送节奏，防止 WinMM buffer 溢出
			if !lastSend.IsZero() && evt.DeltaSec > 0 {
				targetGap := time.Duration(evt.DeltaSec * float64(time.Second))
				actualGap := time.Since(lastSend)
				if targetGap > actualGap {
					wait := targetGap - actualGap
					if wait > 100*time.Millisecond {
						wait = 100 * time.Millisecond
					}
					time.Sleep(wait)
				}
			}

			if err := midiOut.Write(data); err != nil {
				// 详细错误与自愈（后台重建端口）已由 MidiOutput 内部处理
				_ = err
			}
			lastSend = time.Now()
		}
	}()

	go func() {
		for evt := range wsClient.StatusChan {
			switch evt.Type {
			case "connected":
				golog.Info(T("index.connected", nil))
			case "authenticated":
				golog.Info(T("index.authenticated", nil))
			case "auth_failed":
				golog.Error(T("index.authFailed", map[string]string{"reason": evt.Reason}))
			case "kicked":
				midiOut.AllNotesOff()
				key := "index.kicked." + evt.Reason
				msg := T(key, nil)
				if msg == key {
					msg = T("index.kicked", map[string]string{"reason": evt.Reason})
				}
				golog.Warn(msg)
				// reconnectOnKick 语义接线：
				//  false = 被踢后进程直接退出（文档承诺的行为）
				//  true  = 保持运行，重连与否由 reconnect.enabled 决定
				if !cfg.MIDI.ReconnectOnKick {
					golog.Info("reconnectOnKick is disabled, exiting")
					golog.Info("Goodbye.")
					os.Exit(0)
				} else if evt.Reason == "unknown reason" {
					golog.Warn(T("index.kickedHint", nil))
				}
			case "disconnected":
				midiOut.AllNotesOff()
				golog.Warn(T("index.disconnected", map[string]string{"code": strconv.Itoa(evt.Code)}))
			case "reconnecting":
				// 重连进度已由 wsclient 在 reconnectWait 中记录，此处无需重复
			case "max_reconnects":
				// 达到最大重连次数：wsclient 已停止重试，进程保持存活等待用户处理
				midiOut.AllNotesOff()
				golog.Error(T("wsClient.maxReconnects", map[string]string{"max": strconv.Itoa(cfg.Reconnect.MaxAttempts)}))
			}
		}
	}()

	wsClient.Connect(cfg.Server.Host, cfg.Server.Port, cfg.Auth.Password, cfg.Reconnect, cfg.TLS)

	golog.Info("Ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	golog.Info(T("index.shutdown", map[string]string{"signal": sig.String()}))

	midiOut.AllNotesOff()
	wsClient.Disconnect()
	midiOut.Close()

	golog.Info("Goodbye.")
}
