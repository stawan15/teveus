package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Thai rows used to gain a cell when SARA AM was decomposed after the row was
// fitted, and a row short of its column was joined as-is, so the sidebar's
// border sat a cell to the right on those lines only.
func TestBorderStaysOnItsColumn(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.add(&block{kind: kindUser, text: "ทำ playbook ใหม่"})
	m.add(&block{kind: kindAssistant, text: "## สิ่งที่ควรทำ\n\n| สิ่งที่สร้าง | ค่า |\n|---|---|\n| user | logstash UID 984 |\n\nเนื้อความยาว ๆ ที่ต้อง wrap หลายบรรทัด"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"}, state: toolDone})
	m.refresh()
	lines := strings.Split(m.View(), "\n")
	top := 1 + m.tabsHeight()
	for i := top; i < top+m.vp.Height && i < len(lines); i++ {
		if got := ansi.Strip(ansi.Cut(lines[i], m.chatW, m.chatW+1)); got != "│" {
			t.Errorf("row %d: the sidebar border is not in its column (%q)", i, got)
		}
	}
}

// A row a cell short of its column must be padded, not joined as it is.
func TestJoinSidesPadsShortRows(t *testing.T) {
	got := joinSides("abc  \ndef", "|x|\n|y|", 6)
	for i, l := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(l); w != 9 {
			t.Errorf("row %d is %d cells: %q", i, w, l)
		}
	}
}
