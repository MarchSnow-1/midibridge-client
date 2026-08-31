package main

import "testing"

// TestValidMidiMessage 验证下行 MIDI 消息的结构校验逻辑。
func TestValidMidiMessage(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"Note On 3 bytes", []byte{0x90, 60, 100}, true},
		{"Note Off 3 bytes", []byte{0x80, 60, 0}, true},
		{"Program Change 2 bytes", []byte{0xC0, 5}, true},
		{"Channel Pressure 2 bytes", []byte{0xD0, 64}, true},
		{"Pitch Bend 3 bytes", []byte{0xE0, 0, 64}, true},
		{"CC 3 bytes", []byte{0xB0, 7, 100}, true},
		{"Realtime clock 1 byte", []byte{0xF8}, true},
		{"Active sensing 1 byte", []byte{0xFE}, true},
		{"SysEx complete", append([]byte{0xF0, 0x7E, 0x7F, 0x06, 0x01}, 0xF7), true},
		{"empty", nil, false},
		{"data byte first", []byte{0x60, 60, 100}, false},
		{"Note On wrong length", []byte{0x90, 60}, false},
		{"Note On too long", []byte{0x90, 60, 100, 42}, false},
		{"Program Change wrong length", []byte{0xC0, 5, 10}, false},
		{"SysEx unterminated", []byte{0xF0, 0x7E, 0x7F}, false},
		{"SysEx no payload", []byte{0xF0, 0xF7}, true},
		{"undefined status F4", []byte{0xF4}, false},
		{"undefined status F9", []byte{0xF9}, false},
		{"F7 as first byte", []byte{0xF7, 0x00}, false},
		{"Song Position 3 bytes", []byte{0xF2, 0x00, 0x40}, true},
		{"Song Position wrong length", []byte{0xF2, 0x00}, false},
		{"MTC Quarter Frame 2 bytes", []byte{0xF1, 0x00}, true},
	}
	for _, c := range cases {
		if got := validMidiMessage(c.data); got != c.want {
			t.Errorf("%s: validMidiMessage(% X) = %v, want %v", c.name, c.data, got, c.want)
		}
	}
}
