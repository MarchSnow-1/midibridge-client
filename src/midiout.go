package main

import (
	golog "github.com/donnie4w/go-logger/logger"
	"runtime"
	"strings"
	"sync"
	"time"

	"gitlab.com/gomidi/midi"
	driver "gitlab.com/gomidi/rtmididrv"
)

type virtualOutDriver interface {
	OpenVirtualOut(name string) (midi.Out, error)
}

type MidiOutput struct {
	drv       midi.Driver
	out       midi.Out
	name      string
	notesHeld map[byte]bool
	mu        sync.Mutex
}

func NewMidiOutput() *MidiOutput {
	return &MidiOutput{
		notesHeld: make(map[byte]bool),
	}
}

func (m *MidiOutput) HoldNote(note byte) {
	m.mu.Lock()
	m.notesHeld[note] = true
	m.mu.Unlock()
}

func (m *MidiOutput) ReleaseNote(note byte) {
	m.mu.Lock()
	delete(m.notesHeld, note)
	m.mu.Unlock()
}

func (m *MidiOutput) AllNotesOff() {
	m.mu.Lock()
	notes := make([]byte, 0, len(m.notesHeld))
	for note := range m.notesHeld {
		notes = append(notes, note)
	}
	m.notesHeld = make(map[byte]bool)
	m.mu.Unlock()

	for _, note := range notes {
		m.Write([]byte{0x80, note, 0})
	}
}

func (m *MidiOutput) Init(name string) error {
	m.name = name

	var err error
	m.drv, err = driver.New()
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		err = m.initWindows()
	} else {
		err = m.initUnix()
	}
	return err
}

func (m *MidiOutput) initUnix() error {
	vd, ok := m.drv.(virtualOutDriver)
	if !ok {
		return m.initWindows()
	}

	out, err := vd.OpenVirtualOut(m.name)
	if err != nil {
		golog.Error(T("virtualMidi.createFailed", map[string]string{"error": err.Error()}))
		if runtime.GOOS == "darwin" {
			golog.Error(T("virtualMidi.macOSHint", nil))
		} else {
			golog.Error(T("virtualMidi.linuxHint", nil))
		}
		return err
	}

	m.out = out
	golog.Info(T("virtualMidi.created", map[string]string{"name": m.name}))
	return nil
}

func (m *MidiOutput) initWindows() error {
	outs, err := m.drv.Outs()
	if err != nil {
		return err
	}

	if len(outs) == 0 {
		golog.Error(T("virtualMidi.noPorts", nil))
		golog.Error(T("virtualMidi.winHelp1", nil))
		golog.Error(T("virtualMidi.winHelp2", nil))
		golog.Error(T("virtualMidi.winHelp3", nil))
		golog.Error(T("virtualMidi.winHelp4", nil))
		golog.Error(T("virtualMidi.winHelp5", map[string]string{"name": m.name}))
		golog.Error(T("virtualMidi.winHelp6", nil))
		return &midiError{T("virtualMidi.noPortsAvailable", nil)}
	}

	golog.Info(T("virtualMidi.detectedPorts", nil))
	for i, out := range outs {
		golog.Info("  [" + itoa(i) + "] " + out.String())
	}

	idx := findPortByName(outs, m.name)
	if idx < 0 {
		golog.Error(T("virtualMidi.notFound", map[string]string{"name": m.name}))
		golog.Error(T("virtualMidi.notFoundHint1", nil))
		golog.Error(T("virtualMidi.notFoundHint2", nil))
		return &midiError{T("virtualMidi.notFoundError", map[string]string{"name": m.name})}
	}

	m.out = outs[idx]
	if err := m.out.Open(); err != nil {
		return err
	}

	golog.Info(T("virtualMidi.connected", map[string]string{"index": itoa(idx), "name": m.name}))
	return nil
}

func (m *MidiOutput) Write(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.out == nil {
		return nil
	}

	// 跳过 All Notes Off (CC#123) 和 All Sound Off (CC#120)
	if len(data) >= 3 && data[0]&0xF0 == 0xB0 && (data[1] == 123 || data[1] == 120) {
		return nil
	}

	_, err := m.out.Write(data)
	if err != nil {
		golog.Error(T("virtualMidi.sendFailed", map[string]string{"error": err.Error()}))
	}
	return err
}

func (m *MidiOutput) Reinit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reinitLocked()
}

func (m *MidiOutput) reinitLocked() {
	m.closeLocked()
	time.Sleep(200 * time.Millisecond)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := m.Init(m.name); err == nil {
			return
		}
		if attempt < 3 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	golog.Error("MIDI reinit failed after 3 attempts")
}

func (m *MidiOutput) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

func (m *MidiOutput) closeLocked() {
	if m.out != nil {
		m.out.Close()
		m.out = nil
	}
	if m.drv != nil {
		m.drv.Close()
		m.drv = nil
	}
	if m.name != "" {
		golog.Info(T("virtualMidi.closed", map[string]string{"name": m.name}))
	}
}

func findPortByName(outs []midi.Out, name string) int {
	for i, out := range outs {
		if strings.Contains(out.String(), name) {
			return i
		}
	}
	return -1
}

type midiError struct {
	msg string
}

func (e *midiError) Error() string {
	return e.msg
}

var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

func midiVerbose(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	status := data[0]
	msgType := status & 0xF0
	channel := int(status&0x0F) + 1

	switch msgType {
	case 0x80:
		if len(data) < 3 {
			return ""
		}
		note := data[1]
		vel := int(data[2])
		oct := int(note/12) - 1
		name := noteNames[note%12]
		return "CH" + itoa(channel) + " Note Off: " + name + itoa(oct) + " vel=" + itoa(vel)

	case 0x90:
		if len(data) < 3 {
			return ""
		}
		note := data[1]
		vel := int(data[2])
		oct := int(note/12) - 1
		name := noteNames[note%12]
		if vel == 0 {
			return "CH" + itoa(channel) + " Note Off: " + name + itoa(oct) + " (vel=0)"
		}
		return "CH" + itoa(channel) + " Note On:  " + name + itoa(oct) + " vel=" + itoa(vel)

	case 0xB0:
		if len(data) < 3 {
			return ""
		}
		cc := int(data[1])
		if cc == 123 || cc == 120 {
			return ""
		}
		val := int(data[2])
		return "CH" + itoa(channel) + " CC#" + itoa(cc) + " = " + itoa(val)

	case 0xC0:
		prog := int(data[1])
		return "CH" + itoa(channel) + " Program: " + itoa(prog)

	case 0xE0:
		if len(data) < 3 {
			return ""
		}
		lsb := int(data[1])
		msb := int(data[2])
		val := (msb<<7 | lsb) - 8192
		return "CH" + itoa(channel) + " Pitch: " + itoa(val)
	}
	return ""
}
