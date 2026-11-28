package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// With the Right Architectural Rail, joinSides pads/truncates every left row
// to exact m.chatW so Thai SARA AM, emoji and failed-tool badges on the left
// never shift or tear the │ border on column m.chatW.
func TestBorderStaysOnItsColumn(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.add(&block{kind: kindUser, text: "ทำ playbook ใหม่ ⚠ ทดสอบคำสั่งและสำรวจ"})
	m.add(&block{kind: kindAssistant, text: "## สิ่งที่ควรทำ\n\n| สิ่งที่สร้าง | ค่า |\n|---|---|\n| user | logstash UID 984 ⚠ |\n\nเนื้อความยาว ๆ ที่ต้อง wrap หลายบรรทัด ทำ คำ สำ"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls ~/.gemini/bin 2>/dev/null | head; find /opt/homebrew/bin /usr/local/bin ~/.local/bin -maxdepth 1 -name agy"}, result: "exit code 1", state: toolFailed})
	m.refresh()
	lines := strings.Split(m.View(), "\n")
	for i := 0; i < len(lines); i++ {
		want := "│"
		if i == 1 {
			want = "┼"
		}
		if got := ansi.Strip(ansi.Cut(lines[i], m.chatW, m.chatW+1)); got != want {
			t.Errorf("row %d: rail divider is %q, want %q", i, got, want)
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
