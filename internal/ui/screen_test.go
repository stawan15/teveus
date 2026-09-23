package ui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

// Whatever the transcript holds, every frame must be exactly as tall as the
// window and no line may be wider: one overflowing line makes the terminal
// wrap it, which shifts the whole screen (the Thai SARA AM bug).
func TestScreenNeverOverflows(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	mixed := "ดูหน้าตาเว็บเขาทำง่ายๆเอง น้ำ กำลัง · 中文字符和全角标点，测试 · emoji 🎉👩‍💻🇹🇭 · é combining · ພາສາລາວຳ"
	long := "https://example.com/" + strings.Repeat("a-very-long-path-segment/", 20)
	table := "| name | description | notes |\n|---|---|---|\n| " + strings.Repeat("wide cell ", 20) + " | ทำ | 中文 |\n"
	for _, size := range [][2]int{{40, 12}, {80, 24}, {100, 30}, {160, 40}} {
		w, h := size[0], size[1]
		m := New(Config{Dark: true})
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m.add(&block{kind: kindUser, text: mixed + "\n" + long})
		m.add(&block{kind: kindAssistant, text: "## " + mixed + "\n\n" + table + "\n```go\n\tfunc x() { // " + mixed + "\n```\n" + long})
		m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "echo " + mixed + " " + long},
			result: "\x1b[31mred\x1b[0m\t" + mixed + "\n" + long, state: toolDone})
		m.add(&block{kind: kindTool, name: "Edit", input: map[string]any{"file_path": "/x/" + mixed + ".go", "old_string": "a\t" + mixed, "new_string": "b\t" + long}, state: toolDone})
		m.add(&block{kind: kindInfo, text: mixed})
		m.add(&block{kind: kindError, text: long})
		m.add(&block{kind: kindTurnEnd, text: "Worked for 3s · " + mixed})
		m.add(&block{kind: kindAssistant, text: mixed, streaming: true})
		m.refresh()

		check := func(stage string) {
			t.Helper()
			view := m.View()
			if strings.ContainsAny(view, "ำຳ") {
				t.Fatalf("%dx%d %s: raw SARA AM reached the screen", w, h, stage)
			}
			lines := strings.Split(view, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d %s: %d lines, want %d", w, h, stage, len(lines), h)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got > w {
					t.Fatalf("%dx%d %s: line %d is %d wide: %q", w, h, stage, i, got, ansi.Strip(l))
				}
			}
		}
		check("transcript")

		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(mixed)})
		m.Update(newlineKey)
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(long)})
		check("multi-line input")

		in, _ := json.Marshal(map[string]any{"command": strings.Repeat(mixed+" ", 5), "description": mixed})
		m.handleEvent(&claude.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: in, Description: mixed,
			Suggestions: json.RawMessage(`["rule"]`), AlwaysLabel: "always allow `" + mixed + "` commands in this project"})
		m.layout()
		check("permission prompt")
		m.perms = nil
		m.layout()

		m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
		check("palette")
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})

		m.vp.GotoTop()
		m.sel.has, m.sel.anchor, m.sel.head = true, point{0, 0}, point{3, 10}
		check("selection")
	}
}
