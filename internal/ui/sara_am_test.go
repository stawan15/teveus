package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// terminalWidth measures a row the way a terminal draws it. The width libraries
// fold Thai and Lao SARA AM into the previous grapheme and count it as one cell,
// but terminals draw two, so a row they call narrow can still wrap on screen —
// and a wrapped row shifts everything below it, which walks the sidebar border
// in and down the display.
func terminalWidth(s string) int {
	return ansi.StringWidth(amSplitter.Replace(ansi.Strip(s)))
}

// TestSaraAmNeverWraps guards every place a line can come from. The todo list,
// the sidebar's own sections and tool output each build their own text, and the
// bug was that only some of those paths decomposed SARA AM before measuring.
func TestSaraAmNeverWraps(t *testing.T) {
	// Every phrase holds SARA AM (ำ) inside a word, which is where it appears.
	thai := "กำลังทำความสะอาดเครื่องใหม่ให้หน่อย กำลังตรวจสอบ กำลังทำ กำลังทำ"
	todos := map[string]any{"todos": []any{
		map[string]any{"content": thai, "status": "completed"},
		map[string]any{"content": thai, "status": "in_progress"},
		map[string]any{"content": thai, "status": "pending"},
	}}
	cases := map[string]func(m *Model){
		"user":      func(m *Model) { m.add(&block{kind: kindUser, text: thai}) },
		"assistant": func(m *Model) { m.add(&block{kind: kindAssistant, text: thai}) },
		"info":      func(m *Model) { m.add(&block{kind: kindInfo, text: thai}) },
		"error":     func(m *Model) { m.add(&block{kind: kindError, text: thai}) },
		"cmdout":    func(m *Model) { m.add(&block{kind: kindCmdOut, text: thai}) },
		"turnend":   func(m *Model) { m.add(&block{kind: kindTurnEnd, text: thai}) },
		"tool": func(m *Model) {
			m.add(&block{kind: kindTool, name: "Bash", state: toolDone,
				input: map[string]any{"command": thai}, result: thai})
		},
		"todo-block": func(m *Model) {
			m.add(&block{kind: kindTool, name: "TodoWrite", state: toolDone, input: todos})
		},
		"sidebar-plan": func(m *Model) { m.todos = todos },
		"notice":       func(m *Model) { m.note(thai, true) },
	}
	for name, setup := range cases {
		for w := 70; w <= 140; w++ {
			m := New(Config{Dark: true})
			m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
			m.add(&block{kind: kindUser, text: "hello"})
			setup(m)
			m.layout()
			m.refresh()
			for i, row := range strings.Split(m.View(), "\n") {
				if got := terminalWidth(row); got > w {
					t.Fatalf("%s at width %d: row %d draws %d cells, so the terminal wraps it: %q",
						name, w, i, got, ansi.Strip(row))
				}
			}
		}
	}
}
