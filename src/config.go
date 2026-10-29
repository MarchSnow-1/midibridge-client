package main

import (
	"encoding/json"
	"flag"
	golog "github.com/donnie4w/go-logger/logger"
	"os"
	"path/filepath"
	"strconv"
)

type ClientConfig struct {
	Lang      string          `json:"lang"`
	Server    ServerConfig    `json:"server"`
	Auth      AuthConfig      `json:"auth"`
	MIDI      MIDIConfig      `json:"midi"`
	Reconnect ReconnectConfig `json:"reconnect"`
	Logging   LoggingConfig   `json:"logging"`
	TLS       TLSConfig       `json:"tls"`
	IPC       IPCConfig       `json:"ipc"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type AuthConfig struct {
	Password string `json:"password"`
}

// TLSConfig TLS 连接配置。启用后使用 wss:// 加密连接；
// 服务端为自签证书时，通过 CACert 指定其证书（或其 CA）以完成信任。
type TLSConfig struct {
	Enabled bool   `json:"enabled"` // 是否启用 TLS（wss://）
	CACert  string `json:"caCert"`  // 信任的证书文件路径（PEM），用于自签证书
}

type MIDIConfig struct {
	// Enabled 控制是否初始化虚拟 MIDI 输出端口，默认 true 与引入 IPC 之前的行为一致
	// 纯 IPC 模式只把数据流给其他进程而不注入虚拟端口，此时可设为 false，无需安装 loopMIDI 等虚拟端口驱动
	//
	// Enabled controls whether the virtual MIDI output port is initialised; it defaults to true, matching the behaviour before IPC existed
	// In IPC-only mode the stream goes to other processes and no virtual port is injected, so this can be false and no loopMIDI-style driver is needed
	Enabled         bool   `json:"enabled"`
	VirtualPortName string `json:"virtualPortName"`
	ReconnectOnKick bool   `json:"reconnectOnKick"`
}

// IPCConfig 面向其他进程的只读 TCP 数据通道
// 为空表示不启用，此时行为与未引入该功能时一致
//
// IPCConfig is the read-only TCP data channel for other processes
// An empty value disables it, and the client then behaves as it did before the feature existed
type IPCConfig struct {
	// Listen 是监听地址，如 127.0.0.1:9003，端口可写 0 由系统分配
	// 地址由使用者选择，绑定非回环地址时 client 记录安全警告，原因是该接口没有鉴权
	//
	// Listen is the bind address such as 127.0.0.1:9003, and the port may be 0 for the OS to choose
	// The address is the user's choice; binding a non-loopback address logs a security warning because the interface has no authentication
	Listen string `json:"listen"`
}

type ReconnectConfig struct {
	Enabled     bool `json:"enabled"`
	IntervalMs  int  `json:"intervalMs"`
	MaxAttempts int  `json:"maxAttempts"`
}

type LoggingConfig struct {
	File        bool `json:"file"`
	MidiVerbose bool `json:"midiVerbose"`
}

func defaultConfig() ClientConfig {
	return ClientConfig{
		Lang: "en",
		Server: ServerConfig{
			Host: "192.168.1.100",
			Port: 9001,
		},
		Auth: AuthConfig{
			Password: "",
		},
		MIDI: MIDIConfig{
			// 默认启用：保持引入 IPC 之前的行为，普通用户升级后零感知。
			Enabled:         true,
			VirtualPortName: "MIDIBridge",
			ReconnectOnKick: true,
		},
		Reconnect: ReconnectConfig{
			Enabled:     true,
			IntervalMs:  3000,
			MaxAttempts: 0,
		},
		Logging: LoggingConfig{
			File:        false,
			MidiVerbose: false,
		},
		TLS: TLSConfig{
			Enabled: false,
			CACert:  "",
		},
		IPC: IPCConfig{
			// 默认不启用：不传 -ipc-listen 时不监听任何端口。
			Listen: "",
		},
	}
}

func loadConfig(configPath string) (*ClientConfig, error) {
	cfg := defaultConfig()

	// 1. File config (auto-generate on first run)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			golog.Info("First run, generating default config...")
			ensureDir(configPath)
			if err := saveConfig(configPath, &cfg); err != nil {
				golog.Warn("Failed to write default config: " + err.Error())
			}
		}
	} else {
		var fileCfg ClientConfig
		if err := json.Unmarshal(data, &fileCfg); err != nil {
			golog.Warn(T("config.readFailed", map[string]string{"error": err.Error()}))
		} else {
			var rawKeys map[string]json.RawMessage
			json.Unmarshal(data, &rawKeys)
			mergeFile(&cfg, &fileCfg, rawKeys)
		}
	}

	// 2. Environment variables
	mergeEnv(&cfg)

	// 3. CLI flags (registered after file+env so defaults = current merged values)
	flag.StringVar(&cfg.Lang, "lang", cfg.Lang, "Language (en, zh-CN)")
	flag.StringVar(&cfg.Server.Host, "host", cfg.Server.Host, "Server host")
	flag.IntVar(&cfg.Server.Port, "port", cfg.Server.Port, "Server port")
	flag.StringVar(&cfg.Auth.Password, "password", cfg.Auth.Password, "Auth password")
	flag.StringVar(&cfg.MIDI.VirtualPortName, "port-name", cfg.MIDI.VirtualPortName, "Virtual MIDI port name")
	flag.StringVar(&cfg.IPC.Listen, "ipc-listen", cfg.IPC.Listen, "Expose the MIDI stream to local processes over TCP (loopback only, e.g. 127.0.0.1:9003; empty disables)")
	flag.BoolVar(&cfg.MIDI.Enabled, "midi", cfg.MIDI.Enabled, "Initialize the virtual MIDI output port (disable for IPC-only mode)")
	flag.Parse()

	// 安全提示：命令行参数在进程列表与 shell 历史中可见
	passwordViaCLI := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "password" {
			passwordViaCLI = true
		}
	})
	if passwordViaCLI {
		golog.Warn("Security: passing the password via -password exposes it in the process list and shell history; prefer data/config.json")
	}

	// 将相对 caCert 路径锚定到配置文件所在目录（与配置锚点一致），
	// 避免从其他目录启动时按工作目录解析导致证书找不到。
	cfg.TLS.CACert = resolveCaCertPath(configPath, cfg.TLS.CACert)

	return &cfg, nil
}

// resolveCaCertPath 将相对 tls.caCert 路径解析为配置文件所在目录下的绝对路径。
// 配置锚定在可执行文件目录，caCert 与配置同基准，不受进程工作目录影响。
func resolveCaCertPath(configPath, cert string) string {
	if cert == "" || filepath.IsAbs(cert) {
		return cert
	}
	return filepath.Join(filepath.Dir(configPath), cert)
}

// subKeys 从 rawKeys 中提取指定顶层键的子键集合。
// 用于字段级合并：仅当用户显式写了某个子键时才覆盖它，
// 避免"只写一项、其余被重置为零值"的整对象覆盖陷阱。
func subKeys(rawKeys map[string]json.RawMessage, key string) map[string]json.RawMessage {
	raw, ok := rawKeys[key]
	if !ok {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func mergeFile(dst, src *ClientConfig, rawKeys map[string]json.RawMessage) {
	if src.Lang != "" {
		dst.Lang = src.Lang
	}
	if src.Server.Host != "" {
		dst.Server.Host = src.Server.Host
	}
	if src.Server.Port != 0 {
		dst.Server.Port = src.Server.Port
	}
	if src.Auth.Password != "" {
		dst.Auth.Password = src.Auth.Password
	}
	if src.MIDI.VirtualPortName != "" {
		dst.MIDI.VirtualPortName = src.MIDI.VirtualPortName
	}

	// 字段级合并：嵌套对象仅覆盖用户显式写出的子键。
	// 旧行为（整对象覆盖）会让"只写 reconnect.intervalMs"的用户
	// 意外丢失 reconnect.enabled 等其余配置。
	if midiKeys := subKeys(rawKeys, "midi"); midiKeys != nil {
		// Enabled 必须按存在性判断：默认值为 true，若直接比较布尔值，
		// 用户显式写 false 会被零值语义吞掉而永远无法关闭。
		if _, ok := midiKeys["enabled"]; ok {
			dst.MIDI.Enabled = src.MIDI.Enabled
		}
		if _, ok := midiKeys["reconnectOnKick"]; ok {
			dst.MIDI.ReconnectOnKick = src.MIDI.ReconnectOnKick
		}
	}
	if reconnectKeys := subKeys(rawKeys, "reconnect"); reconnectKeys != nil {
		if _, ok := reconnectKeys["enabled"]; ok {
			dst.Reconnect.Enabled = src.Reconnect.Enabled
		}
		if _, ok := reconnectKeys["intervalMs"]; ok {
			dst.Reconnect.IntervalMs = src.Reconnect.IntervalMs
		}
		if _, ok := reconnectKeys["maxAttempts"]; ok {
			dst.Reconnect.MaxAttempts = src.Reconnect.MaxAttempts
		}
	}
	if loggingKeys := subKeys(rawKeys, "logging"); loggingKeys != nil {
		if _, ok := loggingKeys["file"]; ok {
			dst.Logging.File = src.Logging.File
		}
		if _, ok := loggingKeys["midiVerbose"]; ok {
			dst.Logging.MidiVerbose = src.Logging.MidiVerbose
		}
	}
	if tlsKeys := subKeys(rawKeys, "tls"); tlsKeys != nil {
		if _, ok := tlsKeys["enabled"]; ok {
			dst.TLS.Enabled = src.TLS.Enabled
		}
		if _, ok := tlsKeys["caCert"]; ok {
			dst.TLS.CACert = src.TLS.CACert
		}
	}
	if ipcKeys := subKeys(rawKeys, "ipc"); ipcKeys != nil {
		// 同样按存在性判断：Listen 的清空是有意义的状态（关闭 IPC），
		// 若用 !="" 判断，用户无法通过写空串关闭它。
		if _, ok := ipcKeys["listen"]; ok {
			dst.IPC.Listen = src.IPC.Listen
		}
	}
}

func mergeEnv(cfg *ClientConfig) {
	if v := os.Getenv("MIDIBRIDGE_LANG"); v != "" {
		cfg.Lang = v
	}
	if v := os.Getenv("MIDIBRIDGE_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("MIDIBRIDGE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	// 注意：环境变量可被子进程与进程环境检视工具读取，
	// 敏感场景优先使用 0600 权限的配置文件
	if v := os.Getenv("MIDIBRIDGE_PASSWORD"); v != "" {
		cfg.Auth.Password = v
	}
	if v := os.Getenv("MIDIBRIDGE_PORT_NAME"); v != "" {
		cfg.MIDI.VirtualPortName = v
	}
	if v := os.Getenv("MIDIBRIDGE_IPC_LISTEN"); v != "" {
		cfg.IPC.Listen = v
	}
	if v := os.Getenv("MIDIBRIDGE_MIDI_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.MIDI.Enabled = b
		}
	}
}

func saveConfig(configPath string, cfg *ClientConfig) error {
	ensureDir(configPath)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 0600：配置内含明文密码，禁止同机其他用户读取。
	// WriteFile 的权限参数只在新建时生效，对已存在的宽松权限文件
	// 重写后仍需显式 Chmod 收紧。
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	return os.Chmod(configPath, 0600)
}

func ensureDir(path string) {
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0700)
}
