package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// stripPaddingColour may only drop codes around bare blanks. Anything that
// changes the drawn text, its width or a colour that shows is a bug: the whole
// point is that the frame looks the same and only weighs less.
func TestStripPaddingColourKeepsTheDrawing(t *testing.T) {
	kept := []string{
		"\x1b[38;2;232;238;245;48;2;32;40;50m code \x1b[0m", // background
		"\x1b[4m\x1b[38;2;232;238;245mlink\x1b[0m",          // underline
		"\x1b[38;2;232;238;245;1mword\x1b[0m",               // bold
		"\x1b[38;2;232;238;245m• a\x1b[0m",                  // text
	}
	for _, s := range kept {
		got := stripPaddingColour(s)
		if got != s {
			t.Fatalf("changed a drawn line: %q -> %q", s, got)
		}
	}

	// Padding is what goes: a foreground colour set around nothing but spaces.
	// The blanks stay, so the row still fills its column.
	spaced := "reply\x1b[38;2;1;2;3m    \x1b[0m"
	got := stripPaddingColour(spaced)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("padding codes kept: %q", got)
	}
	if ansi.StringWidth(got) != ansi.StringWidth(spaced) {
		t.Fatalf("width changed: %d -> %d", ansi.StringWidth(spaced), ansi.StringWidth(got))
	}
}

// A frame full of replies must not carry a colour code for every blank.
func TestFrameCarriesFewEscapeCodes(t *testing.T) {
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for i := 0; i < 20; i++ {
		m.add(&block{kind: kindAssistant, text: "Here is **markdown** with `code` and a list:\n\n- a\n- b\n\n```go\nfunc x() {}\n```"})
	}
	m.refresh()
	frame := m.View()
	// One code per styled run, not one per padded cell: glamour alone put ~1,300
	// sequences in six lines of reply before this was stripped.
	if n := strings.Count(frame, "\x1b"); n > 1200 {
		t.Fatalf("frame carries %d escape sequences for %d bytes", n, len(frame))
	}
}
