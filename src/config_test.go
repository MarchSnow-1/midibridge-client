package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMergeFileFieldLevel 验证嵌套对象按字段级合并：
// 用户只写部分子键时，其余子键保持默认值不被重置。
func TestMergeFileFieldLevel(t *testing.T) {
	// 场景 1：只写 reconnect.intervalMs —— enabled 等其余字段不得被重置
	raw := []byte(`{"reconnect":{"intervalMs":5000}}`)
	var fileCfg ClientConfig
	if err := json.Unmarshal(raw, &fileCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var rawKeys map[string]json.RawMessage
	json.Unmarshal(raw, &rawKeys)

	dst := defaultConfig()
	mergeFile(&dst, &fileCfg, rawKeys)

	if dst.Reconnect.IntervalMs != 5000 {
		t.Errorf("IntervalMs = %d, want 5000", dst.Reconnect.IntervalMs)
	}
	if !dst.Reconnect.Enabled {
		t.Errorf("Enabled = false, want true (partial object must not reset siblings)")
	}
	if dst.Reconnect.MaxAttempts != 0 {
		t.Errorf("MaxAttempts = %d, want 0", dst.Reconnect.MaxAttempts)
	}

	// 场景 2：只写 logging.midiVerbose —— file 不得被重置
	raw2 := []byte(`{"logging":{"midiVerbose":true}}`)
	var fileCfg2 ClientConfig
	json.Unmarshal(raw2, &fileCfg2)
	var rawKeys2 map[string]json.RawMessage
	json.Unmarshal(raw2, &rawKeys2)

	dst2 := defaultConfig()
	mergeFile(&dst2, &fileCfg2, rawKeys2)

	if !dst2.Logging.MidiVerbose {
		t.Errorf("MidiVerbose = false, want true")
	}
	if dst2.Logging.File {
		t.Errorf("File = true, want false (partial object must not reset siblings)")
	}

	// 场景 3：完整对象仍正常覆盖
	raw3 := []byte(`{"reconnect":{"enabled":false,"intervalMs":1000,"maxAttempts":10}}`)
	var fileCfg3 ClientConfig
	json.Unmarshal(raw3, &fileCfg3)
	var rawKeys3 map[string]json.RawMessage
	json.Unmarshal(raw3, &rawKeys3)

	dst3 := defaultConfig()
	mergeFile(&dst3, &fileCfg3, rawKeys3)

	if dst3.Reconnect.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if dst3.Reconnect.IntervalMs != 1000 {
		t.Errorf("IntervalMs = %d, want 1000", dst3.Reconnect.IntervalMs)
	}
	if dst3.Reconnect.MaxAttempts != 10 {
		t.Errorf("MaxAttempts = %d, want 10", dst3.Reconnect.MaxAttempts)
	}
}

// TestResolveCaCertPath 验证相对 caCert 路径锚定到配置文件所在目录。
func TestResolveCaCertPath(t *testing.T) {
	configPath := filepath.Join("some", "dir", "data", "config.json")

	// 绝对路径原样保留
	abs, err := filepath.Abs("ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveCaCertPath(configPath, abs); got != abs {
		t.Errorf("absolute path changed to %q", got)
	}

	// 空值原样保留
	if got := resolveCaCertPath(configPath, ""); got != "" {
		t.Errorf("empty path changed to %q", got)
	}

	// 相对路径锚定到配置目录
	got := resolveCaCertPath(configPath, "ca.crt")
	want := filepath.Join("some", "dir", "data", "ca.crt")
	if got != want {
		t.Errorf("relative path = %q, want %q", got, want)
	}
}

// TestSaveConfigTightensPerms 验证重写已存在的宽松权限配置文件后权限被收紧到 0600。
func TestSaveConfigTightensPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 权限位在 Windows 上不生效")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	if err := saveConfig(path, &cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

// TestMIDIEnabledDefault 验证未配置 midi.enabled 时保持 true，
// 即升级到含 IPC 功能的版本后，普通用户的虚拟 MIDI 输出行为不变。
func TestMIDIEnabledDefault(t *testing.T) {
	if !defaultConfig().MIDI.Enabled {
		t.Fatal("defaultConfig().MIDI.Enabled = false, want true")
	}

	// 配置文件存在但没写 midi.enabled：仍应为 true。
	raw := []byte(`{"midi":{"virtualPortName":"MyPort"}}`)
	var fileCfg ClientConfig
	if err := json.Unmarshal(raw, &fileCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var rawKeys map[string]json.RawMessage
	json.Unmarshal(raw, &rawKeys)

	dst := defaultConfig()
	mergeFile(&dst, &fileCfg, rawKeys)

	if !dst.MIDI.Enabled {
		t.Error("MIDI.Enabled = false after merging a config without midi.enabled, want true")
	}
	if dst.MIDI.VirtualPortName != "MyPort" {
		t.Errorf("VirtualPortName = %q, want MyPort", dst.MIDI.VirtualPortName)
	}
}

// TestMIDIEnabledExplicitFalse 验证显式写 midi.enabled=false 能被识别。
// 这是纯 IPC 模式的关键开关：布尔零值与"未配置"无法区分，
// 必须靠 subKeys 的存在性检查才能正确生效。
func TestMIDIEnabledExplicitFalse(t *testing.T) {
	raw := []byte(`{"midi":{"enabled":false}}`)
	var fileCfg ClientConfig
	if err := json.Unmarshal(raw, &fileCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var rawKeys map[string]json.RawMessage
	json.Unmarshal(raw, &rawKeys)

	dst := defaultConfig()
	mergeFile(&dst, &fileCfg, rawKeys)

	if dst.MIDI.Enabled {
		t.Error("MIDI.Enabled = true, want false (explicit midi.enabled=false must win)")
	}
}

// TestIPCListenDefault 验证未配置 ipc.listen 时不启用 IPC，
// 保证不传新参数时与改动前行为一致。
func TestIPCListenDefault(t *testing.T) {
	if got := defaultConfig().IPC.Listen; got != "" {
		t.Errorf("defaultConfig().IPC.Listen = %q, want empty (IPC disabled by default)", got)
	}
}

// TestIPCListenPrecedence 验证三级优先级：文件 < 环境变量 < 命令行。
// 直接对比 loadConfig 在此环境中无法注入 flag，因此按 loadConfig 内部
// 的实际顺序分别验证各层的合并行为。
func TestIPCListenPrecedence(t *testing.T) {
	const (
		fromFile = "127.0.0.1:9101"
		fromEnv  = "127.0.0.1:9102"
	)

	// 第 1 层：配置文件写入后生效。
	raw := []byte(`{"ipc":{"listen":"` + fromFile + `"}}`)
	var fileCfg ClientConfig
	if err := json.Unmarshal(raw, &fileCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var rawKeys map[string]json.RawMessage
	json.Unmarshal(raw, &rawKeys)

	cfg := defaultConfig()
	mergeFile(&cfg, &fileCfg, rawKeys)
	if cfg.IPC.Listen != fromFile {
		t.Fatalf("after file merge: IPC.Listen = %q, want %q", cfg.IPC.Listen, fromFile)
	}

	// 第 2 层：环境变量覆盖文件。
	t.Setenv("MIDIBRIDGE_IPC_LISTEN", fromEnv)
	mergeEnv(&cfg)
	if cfg.IPC.Listen != fromEnv {
		t.Fatalf("after env merge: IPC.Listen = %q, want %q", cfg.IPC.Listen, fromEnv)
	}

	// 空的 ipc 对象不得清空已合并的值。
	rawEmpty := []byte(`{"ipc":{}}`)
	var fileCfg2 ClientConfig
	json.Unmarshal(rawEmpty, &fileCfg2)
	var rawKeys2 map[string]json.RawMessage
	json.Unmarshal(rawEmpty, &rawKeys2)
	before := cfg.IPC.Listen
	mergeFile(&cfg, &fileCfg2, rawKeys2)
	if cfg.IPC.Listen != before {
		t.Errorf("empty ipc object cleared listen: got %q, want %q", cfg.IPC.Listen, before)
	}

	// 显式空串必须能关闭 IPC（存在性判断而非 !="" 判断）。
	rawDisable := []byte(`{"ipc":{"listen":""}}`)
	var fileCfg3 ClientConfig
	json.Unmarshal(rawDisable, &fileCfg3)
	var rawKeys3 map[string]json.RawMessage
	json.Unmarshal(rawDisable, &rawKeys3)
	mergeFile(&cfg, &fileCfg3, rawKeys3)
	if cfg.IPC.Listen != "" {
		t.Errorf("explicit empty listen did not disable IPC: got %q", cfg.IPC.Listen)
	}
}

// TestMIDIEnabledEnv 验证 MIDIBRIDGE_MIDI_ENABLED 环境变量能关闭虚拟 MIDI 输出。
func TestMIDIEnabledEnv(t *testing.T) {
	cfg := defaultConfig()

	t.Setenv("MIDIBRIDGE_MIDI_ENABLED", "false")
	mergeEnv(&cfg)
	if cfg.MIDI.Enabled {
		t.Error("MIDI.Enabled = true after MIDIBRIDGE_MIDI_ENABLED=false, want false")
	}

	// 非法值不得改变现状（保持上一次的结果）。
	t.Setenv("MIDIBRIDGE_MIDI_ENABLED", "not-a-bool")
	mergeEnv(&cfg)
	if cfg.MIDI.Enabled {
		t.Error("invalid env value must be ignored, leaving MIDI.Enabled false")
	}
}
