package main

import "strings"

var activeStrings map[string]string

var enStrings = map[string]string{
	"index.starting":                   "MIDIBridge Client {version} starting...",
	"index.targetServer":               "Target server: ws://{host}:{port}",
	"index.connected":                  "Connected to server",
	"index.authenticated":              "Authenticated, receiving MIDI signals",
	"index.authFailed":                 "Authentication failed: {reason}. Check password in data/config.json",
	"index.kicked":                     "Kicked by server: {reason}",
	"index.kicked.server_shutdown":     "Server shutting down",
	"index.kicked.password_changed":    "Password changed, update data/config.json and restart",
	"index.kicked.auth_timeout":        "Authentication timed out",
	"index.disconnected":               "Disconnected from server (code: {code})",
	"index.shutdown":                   "Received {signal}, shutting down...",
	"index.uncaughtException":          "Uncaught exception",
	"index.unhandledRejection":         "Unhandled Promise rejection",
	"index.startupFailed":              "Startup failed",
	"config.readFailed":                "Failed to read config file, using defaults: {error}",
	"wsClient.connecting":              "Connecting to server: {url}",
	"wsClient.connectingFailed":        "Failed to connect {url}: {error}",
	"wsClient.authSent":                "Sent authentication request",
	"wsClient.unparseable":             "Received unparseable message",
	"wsClient.connectionClosed":        "Connection closed (code: {code})",
	"wsClient.error":                   "WebSocket error: {error}",
	"wsClient.authenticated":           "Authenticated, receiving MIDI signals",
	"wsClient.authFailed":              "Authentication failed: {reason}",
	"wsClient.kicked":                  "Kicked by server: {reason}",
	"wsClient.unknownMessage":          "Unknown message type: {type}",
	"wsClient.maxReconnects":           "Max reconnect attempts reached ({max}), stopping",
	"wsClient.reconnecting":            "Reconnecting in {delay}s (attempt {attempt})...",
	"wsClient.stateChange":             "State change: {old} → {new}",
	"virtualMidi.created":              "Virtual MIDI port created: \"{name}\"",
	"virtualMidi.createFailed":         "Failed to create virtual MIDI port: {error}",
	"virtualMidi.macOSHint":            "macOS: Enable IAC Driver in \"Audio MIDI Setup\"",
	"virtualMidi.linuxHint":            "Linux: Run sudo modprobe snd-virmidi",
	"virtualMidi.noPorts":              "No MIDI output ports detected",
	"virtualMidi.winHelp1":             "Windows users: create a virtual MIDI port with loopMIDI:",
	"virtualMidi.winHelp2":             "  1. Download and install loopMIDI:",
	"virtualMidi.winHelp3":             "     https://www.tobias-erichsen.de/software/loopmidi.html",
	"virtualMidi.winHelp4":             "  2. Open loopMIDI, click \"+\" to add a port",
	"virtualMidi.winHelp5":             "  3. Set the port name to \"{name}\"",
	"virtualMidi.winHelp6":             "  4. Restart this program",
	"virtualMidi.noPortsAvailable":     "No MIDI output ports available",
	"virtualMidi.detectedPorts":        "Detected MIDI output ports:",
	"virtualMidi.connected":            "Connected to MIDI output port [{index}]: \"{name}\"",
	"virtualMidi.notFound":             "Port \"{name}\" not found",
	"virtualMidi.notFoundHint1":        "Open loopMIDI, add a port matching the configured name, then restart this program",
	"virtualMidi.notFoundHint2":        "Or change midi.virtualPortName in data/config.json to one of the ports listed above",
	"virtualMidi.notFoundError":        "MIDI port \"{name}\" does not exist",
	"virtualMidi.notInit":              "MIDI port not initialized, cannot send message",
	"virtualMidi.sendFailed":           "Failed to send MIDI message: {error}",
	"virtualMidi.closed":               "MIDI port closed: \"{name}\"",
}

var zhCNStrings = map[string]string{
	"index.starting":                   "MIDIBridge Client {version} 启动中...",
	"index.targetServer":               "目标服务端: ws://{host}:{port}",
	"index.connected":                  "已连接到服务端",
	"index.authenticated":              "认证成功，开始接收 MIDI 信号",
	"index.authFailed":                 "认证失败: {reason}，请检查 data/config.json 中的密码配置",
	"index.kicked":                     "被服务端踢出: {reason}",
	"index.kicked.server_shutdown":     "服务端已关闭",
	"index.kicked.password_changed":    "密码已修改，请更新 data/config.json 中的密码后重启",
	"index.kicked.auth_timeout":        "认证超时",
	"index.disconnected":               "与服务端断开连接 (code: {code})",
	"index.shutdown":                   "收到 {signal} 信号，正在关闭...",
	"index.uncaughtException":          "未捕获的异常",
	"index.unhandledRejection":         "未处理的 Promise 拒绝",
	"index.startupFailed":              "启动失败",
	"config.readFailed":                "配置文件读取失败，使用默认值: {error}",
	"wsClient.connecting":              "正在连接服务端: {url}",
	"wsClient.connectingFailed":        "连接 {url} 时 WebSocket 错误: {error}",
	"wsClient.authSent":                "已发送认证请求",
	"wsClient.unparseable":             "收到无法解析的消息",
	"wsClient.connectionClosed":        "连接断开 (code: {code})",
	"wsClient.error":                   "WebSocket 错误: {error}",
	"wsClient.authenticated":           "认证成功，开始接收 MIDI 信号",
	"wsClient.authFailed":              "认证失败: {reason}",
	"wsClient.kicked":                  "被服务端踢出: {reason}",
	"wsClient.unknownMessage":          "未知消息类型: {type}",
	"wsClient.maxReconnects":           "已达到最大重连次数 ({max})，停止重连",
	"wsClient.reconnecting":            "{delay} 秒后进行第 {attempt} 次重连...",
	"wsClient.stateChange":             "状态变更: {old} → {new}",
	"virtualMidi.created":              "虚拟 MIDI 端口已创建: \"{name}\"",
	"virtualMidi.createFailed":         "无法创建虚拟 MIDI 端口: {error}",
	"virtualMidi.macOSHint":            "macOS: 请在\"音频 MIDI 设置\"中启用 IAC Driver",
	"virtualMidi.linuxHint":            "Linux: 请运行 sudo modprobe snd-virmidi",
	"virtualMidi.noPorts":              "未检测到任何 MIDI 输出端口",
	"virtualMidi.winHelp1":             "Windows 用户请按以下步骤创建虚拟 MIDI 端口:",
	"virtualMidi.winHelp2":             "  1. 下载安装 loopMIDI:",
	"virtualMidi.winHelp3":             "     https://www.tobias-erichsen.de/software/loopmidi.html",
	"virtualMidi.winHelp4":             "  2. 打开 loopMIDI，点击左下角 \"+\" 添加端口",
	"virtualMidi.winHelp5":             "  3. 将端口名称设为 \"{name}\"",
	"virtualMidi.winHelp6":             "  4. 重新启动本程序",
	"virtualMidi.noPortsAvailable":     "没有可用的 MIDI 输出端口",
	"virtualMidi.detectedPorts":        "检测到以下 MIDI 输出端口:",
	"virtualMidi.connected":            "已连接 MIDI 输出端口 [{index}]: \"{name}\"",
	"virtualMidi.notFound":             "未找到名为 \"{name}\" 的端口",
	"virtualMidi.notFoundHint1":        "请打开 loopMIDI，添加一个与配置名称匹配的端口，然后重启本程序",
	"virtualMidi.notFoundHint2":        "或在 data/config.json 中修改 midi.virtualPortName 为上列端口之一",
	"virtualMidi.notFoundError":        "MIDI 端口 \"{name}\" 不存在",
	"virtualMidi.notInit":              "MIDI 端口未初始化，无法发送消息",
	"virtualMidi.sendFailed":           "MIDI 消息发送失败: {error}",
	"virtualMidi.closed":               "MIDI 端口已关闭: \"{name}\"",
}

func initI18N(lang string) {
	switch lang {
	case "zh-CN":
		activeStrings = zhCNStrings
	default:
		activeStrings = enStrings
	}
}

func T(key string, params map[string]string) string {
	template := activeStrings[key]
	if template == "" {
		template = enStrings[key]
	}
	if template == "" {
		return key
	}
	for k, v := range params {
		template = strings.ReplaceAll(template, "{"+k+"}", v)
	}
	return template
}
