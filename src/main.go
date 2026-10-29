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
	midiReady := false

	// 先建立 IPC 监听再初始化 MIDI，这样 MIDI 初始化失败时可以据 ipc 是否为空决定退出还是降级
	// IPC 监听失败属于配置错误，静默降级会让使用者以为功能在运行，因此直接退出
	//
	// The IPC listener is created before MIDI initialisation so a MIDI failure can be resolved by whether ipc is nil, exiting or degrading
	// An IPC listen failure is a configuration error; degrading silently would leave the user thinking the feature runs, so the process exits
	ipc, err := NewIPCServer(cfg.IPC.Listen, version)
	if err != nil {
		// 区分两类失败以免误导，地址不合法属于配置问题，地址合法但端口被占用属于环境问题
		//
		// The two failure classes are kept apart to avoid misleading output: an invalid address is a configuration problem, while a valid address with a busy port is environmental
		key := "ipc.listenFailed"
		if isIPCAddrError(err) {
			key = "ipc.nonLoopback"
		}
		golog.Error(T(key, map[string]string{"error": err.Error()}))
		os.Exit(1)
	}
	// 监听成功时 NewIPCServer 内部已打印 "IPC listening on <addr>"，此处不重复记录
	// On success NewIPCServer already logs "IPC listening on <addr>", so it is not logged again here

	switch {
	case !cfg.MIDI.Enabled:
		// 使用者显式关闭虚拟 MIDI 输出，运行在纯 IPC 模式
		// The user turned the virtual MIDI output off, so the client runs in IPC-only mode
		golog.Info(T("midi.disabled", nil))
	default:
		if err := midiOut.Init(cfg.MIDI.VirtualPortName); err != nil {
			if ipc == nil {
				// 未启用 IPC 时保持引入该功能之前的行为，初始化失败即退出
				// With IPC disabled the behaviour predating the feature is kept: a failed init exits
				golog.Error("MIDI init failed: " + err.Error())
				os.Exit(1)
			}
			// 启用了 IPC 时数据流仍有出口，降级为纯 IPC 模式继续运行
			// With IPC enabled the stream still has an outlet, so the client degrades to IPC-only mode and keeps running
			golog.Warn(T("midi.initFailedContinuing", map[string]string{"error": err.Error()}))
		} else {
			midiReady = true
		}
	}

	wsClient := NewWSClient()

	verboseLog := cfg.Logging.MidiVerbose

	go func() {
		var lastSend time.Time
		for evt := range wsClient.MidiChan {
			data := evt.Data

			// 先把数据交给 IPC，必须放在下面的节流 sleep 之前，否则其他进程会被虚拟端口的时序节流拖慢
			// ipc 为 nil 时 Publish 直接返回，无需在此判空
			//
			// Data goes to IPC first and before the pacing sleep below, otherwise other processes would be delayed by the virtual-port pacing
			// When ipc is nil Publish returns immediately, so no nil check is needed here
			ipc.Publish(data)

			// 按通道与音符的复合键跟踪按下的音符，仅在虚拟端口就绪时记账
			// 未就绪时没有端口需要静音，记账只会让 notesHeld 持续增长
			//
			// Held notes are tracked by a channel and note composite key and only recorded when the virtual port is ready
			// With the port unavailable there is nothing to silence, and recording would let notesHeld grow without bound
			if midiReady && len(data) >= 3 {
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

			// 虚拟 MIDI 端口未就绪时跳过节流与写入，节流只为防止 WinMM 缓冲区溢出而存在，没有输出端口时等待没有意义
			//
			// With the virtual port unavailable the pacing and the write are skipped; the pacing exists to avoid overrunning the WinMM buffer and waiting serves no purpose without an output port
			if !midiReady {
				continue
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
		// allNotesOff 仅在虚拟端口就绪时调用，纯 IPC 模式下 midiOut 未初始化，AllNotesOff 会逐音符打印 "MIDI port not initialized" 警告且没有端口需要静音
		//
		// allNotesOff is called only when the virtual port is ready; in IPC-only mode midiOut is not initialised, so AllNotesOff would log "MIDI port not initialized" per held note while no port needs silencing
		allNotesOff := func() {
			if midiReady {
				midiOut.AllNotesOff()
			}
		}
		for evt := range wsClient.StatusChan {
			switch evt.Type {
			case "connected":
				golog.Info(T("index.connected", nil))
			case "authenticated":
				golog.Info(T("index.authenticated", nil))
			case "auth_failed":
				golog.Error(T("index.authFailed", map[string]string{"reason": evt.Reason}))
			case "kicked":
				allNotesOff()
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
				allNotesOff()
				golog.Warn(T("index.disconnected", map[string]string{"code": strconv.Itoa(evt.Code)}))
			case "reconnecting":
				// 重连进度已由 wsclient 在 reconnectWait 中记录，此处无需重复
			case "max_reconnects":
				// 达到最大重连次数：wsclient 已停止重试，进程保持存活等待用户处理
				allNotesOff()
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

	// 纯 IPC 模式下 midiOut 未初始化，这些调用只会对不存在的端口打印 "MIDI port not initialized"，因此统一按 midiReady 收口
	//
	// In IPC-only mode midiOut is not initialised, so these calls would only log "MIDI port not initialized" for a port that never opened, hence the midiReady guard
	if midiReady {
		midiOut.AllNotesOff()
	}
	// IPC 须在 wsClient.Disconnect() 之前关闭，否则 WebSocket 仍在推送时 IPC 已停止接收，那些帧会计入丢弃计数
	//
	// IPC is closed before wsClient.Disconnect(); otherwise the WebSocket is still pushing while IPC has stopped accepting, and those frames would count as drops
	ipc.Close()
	wsClient.Disconnect()
	if midiReady {
		midiOut.Close()
	}

	golog.Info("Goodbye.")
}
