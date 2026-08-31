package main

import "testing"

// TestTargetServerScheme 验证 targetServer 按实际协议渲染 scheme：
// TLS 启用显示 wss://，否则 ws://（防止回归到硬编码 ws://）。
func TestTargetServerScheme(t *testing.T) {
	initI18N("en")

	got := T("index.targetServer", map[string]string{"scheme": "wss", "host": "1.2.3.4", "port": "9001"})
	if want := "Target server: wss://1.2.3.4:9001"; got != want {
		t.Errorf("en wss: got %q, want %q", got, want)
	}

	got = T("index.targetServer", map[string]string{"scheme": "ws", "host": "localhost", "port": "9001"})
	if want := "Target server: ws://localhost:9001"; got != want {
		t.Errorf("en ws: got %q, want %q", got, want)
	}

	initI18N("zh-CN")
	got = T("index.targetServer", map[string]string{"scheme": "wss", "host": "1.2.3.4", "port": "9001"})
	if want := "目标服务端: wss://1.2.3.4:9001"; got != want {
		t.Errorf("zh wss: got %q, want %q", got, want)
	}
}
