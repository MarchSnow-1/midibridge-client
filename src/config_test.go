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
