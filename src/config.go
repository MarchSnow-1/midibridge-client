package main

import (
	golog "github.com/donnie4w/go-logger/logger"
	"encoding/json"
	"flag"
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
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type AuthConfig struct {
	Password string `json:"password"`
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

	return &cfg, nil
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
	if _, ok := rawKeys["midi"]; ok {
		dst.MIDI.ReconnectOnKick = src.MIDI.ReconnectOnKick
	}
	if _, ok := rawKeys["reconnect"]; ok {
		dst.Reconnect = src.Reconnect
	}
	if _, ok := rawKeys["logging"]; ok {
		dst.Logging = src.Logging
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
	return os.WriteFile(configPath, data, 0644)
}

func ensureDir(path string) {
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)
}
