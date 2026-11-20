package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// BenchmarkLongSession measures one streamed chunk in a 6000-block session:
// the transcript is rebuilt on every chunk, so this must stay well under a
// frame's worth of time.
//
//	go test ./internal/ui -run XXX -bench LongSession
func BenchmarkLongSession(b *testing.B) {
	b.Setenv("TEVEUS_CONFIG", b.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	for i := 0; i < 2000; i++ {
		m.add(&block{kind: kindUser, text: fmt.Sprintf("question %d", i)})
		m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "go test ./..."}, result: strings.Repeat("ok line\n", 20), state: toolDone})
		m.add(&block{kind: kindAssistant, text: "Here is **markdown** with `code` and a list:\n\n- a\n- b\n\n```go\nfunc x() {}\n```"})
	}
	m.refresh()
	stream := &block{kind: kindAssistant, streaming: true}
	m.add(stream)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream.text += "word "
		stream.invalidate()
		m.refresh()
		_ = m.View()
	}
}

// The numbers that answer "is it fast, and does it stay fast as the session
// grows?" — the three things a keystroke does (type, move the cursor, scroll)
// and the streaming path:
//
//	go test ./internal/ui -run XXX -bench 'Keystroke|CursorMove|ScrollWheel|LongSession' -benchtime 200x

// BenchmarkKeystroke is what typing costs: one key, one frame.
func BenchmarkKeystroke(b *testing.B) {
	b.Setenv("TEVEUS_CONFIG", b.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	for i := 0; i < 200; i++ {
		m.add(&block{kind: kindUser, text: "question"})
		m.add(&block{kind: kindAssistant, text: "Here is **markdown** with `code`.\n\n- a\n- b\n"})
	}
	m.refresh()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		_ = m.View()
	}
}

// BenchmarkCursorMove is an arrow key: no text changes, only the cursor. It
// must not rebuild the transcript, only the input box.
func BenchmarkCursorMove(b *testing.B) {
	b.Setenv("TEVEUS_CONFIG", b.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	for i := 0; i < 200; i++ {
		m.add(&block{kind: kindUser, text: "question"})
		m.add(&block{kind: kindAssistant, text: "Here is **markdown** with `code`.\n\n- a\n- b\n"})
	}
	m.input.SetValue("some text in the box")
	m.refresh()
	_ = m.View()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		_ = m.View()
	}
}

// BenchmarkScrolledSolve is a long conversation scrolled up: the sidebar and
// the frame are unchanged, only the visible rows differ.
func BenchmarkScrollWheel(b *testing.B) {
	b.Setenv("TEVEUS_CONFIG", b.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	for i := 0; i < 400; i++ {
		m.add(&block{kind: kindUser, text: "question"})
		m.add(&block{kind: kindAssistant, text: "Here is **markdown** with `code`.\n\n- a\n- b\n"})
	}
	m.refresh()
	_ = m.View()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
		_ = m.View()
	}
}
