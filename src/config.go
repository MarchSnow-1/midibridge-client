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
	VirtualPortName string `json:"virtualPortName"`
	ReconnectOnKick bool   `json:"reconnectOnKick"`
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
