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
