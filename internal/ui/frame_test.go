package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestFrameAlwaysFits is the invariant the clamp exists for: whatever the
// state, the frame is exactly the window's size, so the terminal never wraps a
// row or scrolls, which is what leaves a stale status bar on screen.
func TestFrameAlwaysFits(t *testing.T) {
	thai := "กำลังทำความสะอาดเครื่องใหม่ให้หน่อย กำลังตรวจสอบ กำลังทำ กำลังทำ"
	long := strings.Repeat(": Bash cd ~/teveus && gofmt -l . ; ", 6)
	states := map[string]func(m *Model){
		"plain":   func(m *Model) {},
		"busy":    func(m *Model) { m.busy, m.phase = true, "Preparing Bash" },
		"thai-in": func(m *Model) { m.input.SetValue(thai); m.afterInput() },
		"long-in": func(m *Model) { m.input.SetValue(long); m.afterInput() },
		"both": func(m *Model) {
			m.busy, m.phase = true, "Running Bash"
			m.input.SetValue(thai + "\n" + long)
			m.afterInput()
		},
		"slider": func(m *Model) {
			m.pop = popup{mode: popSlider, title: thai, hint: thai,
				all: []choice{{label: thai}, {label: thai}, {label: thai}}}
			m.pop.filter()
		},
		"palette": func(m *Model) {
			m.pop = popup{mode: popPalette, title: thai, all: m.slashChoices()}
			m.pop.filter()
		},
	}
	for name, setup := range states {
		for w := 60; w <= 200; w += 7 {
			for _, h := range []int{20, 30, 48} {
				m := New(Config{Dark: true})
				m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m.add(&block{kind: kindUser, text: thai})
				m.add(&block{kind: kindTool, name: "TodoWrite", state: toolDone,
					input: map[string]any{"todos": []any{
						map[string]any{"content": thai, "status": "completed"}}}})
				m.todos = map[string]any{"todos": []any{
					map[string]any{"content": thai, "status": "pending"}}}
				setup(m)
				m.layout()
				m.refresh()
				rows := strings.Split(m.View(), "\n")
				if len(rows) != h {
					t.Fatalf("%s %dx%d: %d rows, want %d", name, w, h, len(rows), h)
				}
				for i, r := range rows {
					if got := ansi.StringWidth(r); got != w {
						t.Fatalf("%s %dx%d: row %d is %d cells, want %d: %q",
							name, w, h, i, got, w, ansi.Strip(r))
					}
				}
			}
		}
	}
}
