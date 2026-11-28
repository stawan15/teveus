package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func newSelModel(t *testing.T) (*Model, *string) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	var copied string
	clipboardWrite = func(s string) { copied = s }
	t.Cleanup(func() { clipboardWrite = copyToClipboard })
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.add(&block{kind: kindUser, text: "please fix the bug"})
	m.add(&block{kind: kindAssistant, text: "Here is the fix:\n\n```go\nfunc add(a, b int) int {\n\treturn a + b\n}\n```"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "go test ./..."}, result: "ok", state: toolDone})
	for i := 0; i < 30; i++ {
		m.add(&block{kind: kindInfo, text: "filler line"})
	}
	m.refresh()
	m.vp.GotoTop()
	return m, &copied
}

func mouse(m *Model, action tea.MouseAction, btn tea.MouseButton, x, y int) {
	m.Update(tea.MouseMsg{X: x, Y: y, Action: action, Button: btn})
}

// find returns the screen position (x, y) of text in the rendered view.
func find(t *testing.T, m *Model, text string) (int, int) {
	for y, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(l, text); i >= 0 {
			return ansi.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", text, ansi.Strip(m.View()))
	return 0, 0
}

func TestDragSelectCopiesDedentedText(t *testing.T) {
	m, copied := newSelModel(t)
	x1, y1 := find(t, m, "func add")
	x2, y2 := find(t, m, "}")
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x1, y1)
	mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, x2, y2)
	if !m.sel.has || !strings.Contains(m.View(), "\x1b[") {
		t.Fatal("no selection highlight")
	}
	mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x2, y2)
	want := "func add(a, b int) int {\n\treturn a + b\n}"
	if *copied != want {
		t.Fatalf("copied %q, want %q", *copied, want)
	}
}

func TestDoubleAndTripleClick(t *testing.T) {
	m, copied := newSelModel(t)
	x, y := find(t, m, "please")
	for i := 0; i < 2; i++ {
		mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x+7, y) // on "fix"
		mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x+7, y)
	}
	if *copied != "fix" {
		t.Fatalf("double-click copied %q", *copied)
	}
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x+7, y)
	mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x+7, y)
	if !strings.HasSuffix(*copied, "please fix the bug") {
		t.Fatalf("triple-click copied %q", *copied)
	}
}

func TestDragPastEdgeAutoScrolls(t *testing.T) {
	m, copied := newSelModel(t)
	x, y := find(t, m, "please")
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
	mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, 5, m.vp.Height+3) // below the chat
	before := m.vp.YOffset
	for i := 0; i < 5; i++ {
		m.Update(tickMsg{})
	}
	if m.vp.YOffset <= before {
		t.Fatalf("did not scroll: %d -> %d", before, m.vp.YOffset)
	}
	mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, 5, m.vp.Height+3)
	if !strings.Contains(*copied, "please fix the bug") || !strings.Contains(*copied, "filler line") {
		t.Fatalf("copied %q", *copied)
	}
}

func TestClickWithoutDragOpensTool(t *testing.T) {
	m, copied := newSelModel(t)
	x, y := find(t, m, "● Bash") // the transcript card, not the sidebar entry
	var tool *block
	for _, b := range m.blocks {
		if b.kind == kindTool {
			tool = b
		}
	}
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
	mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x, y)
	if !tool.open || *copied != "" {
		t.Fatalf("open=%v copied=%q", tool.open, *copied)
	}
}

func TestCtrlCCopiesSelectionInsteadOfQuitting(t *testing.T) {
	m, copied := newSelModel(t)
	x, y := find(t, m, "please")
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
	mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, x+5, y)
	*copied = ""
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if *copied != "please" || m.sel.has || cmd != nil {
		t.Fatalf("copied=%q has=%v cmd=%v", *copied, m.sel.has, cmd != nil)
	}
}

func TestCopyJoinsLinesWrappedForTheScreen(t *testing.T) {
	m, _ := newSelModel(t)
	m.blocks = nil
	long := "go test ./internal/agent -run TestPlanModeRefusesEdits -v --count=1 -timeout 30s -race -cover ./..."
	m.add(&block{kind: kindAssistant, text: "```sh\n" + long + "\n```"})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.refresh()
	m.vp.GotoTop()
	m.sel.has, m.sel.anchor, m.sel.head = true, point{0, 0}, point{len(m.lines) - 1, 200}
	if got := m.selectedText(); got != long {
		t.Fatalf("wrapped code not joined:\n%q\nwant %q", got, long)
	}
}

func TestHyphenBreak(t *testing.T) {
	for _, c := range []struct {
		line, next string
		want       bool
	}{
		{"go test -v --", "count=1", true},
		{"... -timeout 30s -", "race", true},
		{"point{len(m.lines) -", "1, 200}", false},
		{"a well-", "known", true},
		{"no hyphen", "here", false},
	} {
		if got := hyphenBreak(c.line, c.next); got != c.want {
			t.Errorf("hyphenBreak(%q, %q) = %v", c.line, c.next, got)
		}
	}
}

func TestCopyRestoresTabIndent(t *testing.T) {
	m, _ := newSelModel(t)
	m.blocks = nil
	code := "func f() {\n\tif x {\n\t\treturn\n\t}\n}"
	m.add(&block{kind: kindAssistant, text: "```go\n" + code + "\n```"})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	m.refresh()
	m.sel.has, m.sel.anchor, m.sel.head = true, point{0, 0}, point{len(m.lines) - 1, 200}
	if got := m.selectedText(); got != code {
		t.Fatalf("got %q, want %q", got, code)
	}
}

func TestCopyStripsLeftGutterBorders(t *testing.T) {
	m, _ := newSelModel(t)
	m.blocks = nil
	m.add(&block{kind: kindUser, text: "line one\nline two"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "echo hi"}, result: "alpha\nbeta", state: toolDone})
	m.add(&block{kind: kindCmdOut, text: "cmd line 1\n\ncmd line 2"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.refresh()
	m.vp.GotoTop()

	// Select across all lines starting from col 0 (covering every left gutter).
	m.sel.has, m.sel.anchor, m.sel.head = true, point{0, 0}, point{len(m.lines) - 1, 200}
	got := m.selectedText()
	for _, bar := range []string{"│", "┃", "▎", "▏"} {
		if strings.Contains(got, bar) {
			t.Fatalf("copied text still contains left gutter %q:\n%q", bar, got)
		}
	}
	for _, want := range []string{"line one", "line two", "alpha", "beta", "cmd line 1", "cmd line 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("copied text missing %q:\n%q", want, got)
		}
	}

	// Highlight in chatView must not paint over the gutter bar itself.
	view := m.chatView()
	for _, row := range strings.Split(view, "\n") {
		plain := ansi.Strip(row)
		if !strings.Contains(plain, "│") && !strings.Contains(plain, "┃") {
			continue
		}
		// The gutter bar should appear before the highlight background escape.
		if idx := strings.Index(row, "│"); idx >= 0 {
			if strings.Contains(row[:idx], "\x1b[48;") {
				t.Fatalf("highlight painted over │ gutter: %q", row)
			}
		}
	}
}

func TestTripleClickSkipsLeftGutter(t *testing.T) {
	m, copied := newSelModel(t)
	m.blocks = nil
	m.add(&block{kind: kindUser, text: "hello world"})
	m.add(&block{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"}, result: "README.md", state: toolDone})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.refresh()
	m.vp.GotoTop()

	x, y := find(t, m, "README.md")
	for i := 0; i < 3; i++ {
		mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
		mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x, y)
	}
	if *copied != "README.md" {
		t.Fatalf("triple-click on tool output copied %q, want %q", *copied, "README.md")
	}
}

func TestGutterWidthPreservesMarkdownTables(t *testing.T) {
	if got := gutterWidth(" │ col1 │ col2 │", false); got != 0 {
		t.Fatalf("markdown table row should not be treated as gutter, got %d", got)
	}
	if got := gutterWidth(" │ quoted text", false); got != 3 {
		t.Fatalf("blockquote gutter = %d, want 3", got)
	}
	if got := gutterWidth("   │ tool output", true); got != 5 {
		t.Fatalf("tool gutter = %d, want 5", got)
	}
	if got := gutterWidth(" ┃ user prompt", true); got != 3 {
		t.Fatalf("user gutter = %d, want 3", got)
	}
}
