package main

import (
	golog "github.com/donnie4w/go-logger/logger"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
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

	configPath := filepath.Join(".", "data", "config.json")
	cfg, err := loadConfig(configPath)
	if err != nil {
		golog.Error("Startup failed: " + err.Error())
		os.Exit(1)
	}

	initI18N(cfg.Lang)

	golog.Info(T("index.targetServer", map[string]string{"host": cfg.Server.Host, "port": itoa(cfg.Server.Port)}))

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

			// 跟踪按下的音符
			if len(data) >= 3 {
				switch data[0] & 0xF0 {
				case 0x90:
					if data[2] > 0 {
						midiOut.HoldNote(data[1])
					} else {
						midiOut.ReleaseNote(data[1])
					}
				case 0x80:
					midiOut.ReleaseNote(data[1])
				}
			}

			if verboseLog {
				if s := midiVerbose(data); s != "" {
					golog.Info(s)
				}
			}

			// 按 delta 时间控制发送节奏，防止 WinMM buffer 溢出
			if !lastSend.IsZero() && evt.DeltaMs > 0 {
				targetGap := time.Duration(evt.DeltaMs * float64(time.Second))
				actualGap := time.Since(lastSend)
				if targetGap > actualGap {
					wait := targetGap - actualGap
					if wait > 100*time.Millisecond {
						wait = 100 * time.Millisecond
					}
					time.Sleep(wait)
				}
			}

			midiOut.Write(data)
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
				if evt.Reason == "" {
					if cfg.MIDI.ReconnectOnKick {
						golog.Warn(T("index.kickedHint", nil))
					}
				}
			case "disconnected":
				midiOut.AllNotesOff()
				golog.Warn(T("index.disconnected", map[string]string{"code": itoa(evt.Code)}))
			}
		}
	}()

	wsClient.Connect(cfg.Server.Host, cfg.Server.Port, cfg.Auth.Password, cfg.Reconnect)

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
