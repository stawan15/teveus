package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestLinkify(t *testing.T) {
	const a = "https://example.com/a?b=1"
	got := linkify("see "+a+".", "see "+a+".")
	if !strings.Contains(got, ";"+a+"\x1b\\"+a+"\x1b]8;;\x1b\\.") || ansi.Strip(got) != "see "+a+"." {
		t.Errorf("the full stop belongs after the link: %q", got)
	}
	if got := linkify("no link here", "no link here"); got != "no link here" {
		t.Errorf("changed a line without links: %q", got)
	}
	if got := linkify("(https://example.com/x)", "(https://example.com/x)"); !strings.Contains(got, "https://example.com/x\x1b]8;;\x1b\\)") {
		t.Errorf("a closing bracket belongs after the link: %q", got)
	}
	wiki := "https://en.wikipedia.org/wiki/A_(b)"
	if got := linkify(wiki, wiki); !strings.Contains(got, ";"+wiki+"\x1b\\") {
		t.Errorf("brackets inside the address are kept: %q", got)
	}
}

func TestLongLinkKeepsItsWholeAddress(t *testing.T) {
	u := "https://example.com/" + strings.Repeat("abcdefghij/", 12)
	// How the markdown renderer leaves it: broken at a dot, padded lines.
	out := "  \x1b[4mhttps://example.\x1b[0m" + strings.Repeat(" ", 30) + "\n  \x1b[4m" + u[len("https://example."):] + "\x1b[0m\n  now"
	got := linkify(out, "open "+u+" now")
	lines := strings.Split(got, "\n")
	for _, l := range lines[:2] {
		if !strings.Contains(l, ";"+u+"\x1b\\") {
			t.Errorf("a piece doesn't point at the whole address: %q", l)
		}
	}
	if strings.Contains(lines[2], "\x1b]8;") || ansi.Strip(got) != ansi.Strip(out) {
		t.Errorf("text around the link changed: %q", got)
	}
}

func TestTranscriptLinksAreClickableAndKeepTheirWidth(t *testing.T) {
	m, _ := newSelModel(t)
	m.add(&block{kind: kindAssistant, text: "Docs: [guide](https://example.com/guide) and https://example.com/plain."})
	m.refresh()
	m.vp.GotoBottom()
	view := m.View()
	for _, u := range []string{"https://example.com/guide", "https://example.com/plain"} {
		if !strings.Contains(view, ";"+u+"\x1b\\") {
			t.Errorf("no hyperlink for %s", u)
		}
	}
	for i, l := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(l); w > m.w {
			t.Errorf("line %d is %d wide in a %d window", i, w, m.w)
		}
	}
	if strings.Contains(ansi.Strip(view), "\x1b") || strings.Contains(ansi.Strip(view), "]8;") {
		t.Error("hyperlink escapes leak into copied text")
	}
}

func TestLostReleaseDoesNotKeepScrollingUp(t *testing.T) {
	m, _ := newSelModel(t)
	m.vp.GotoBottom()
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, 10, 5)
	// the release never arrives; the pointer moves to the header with no button held
	mouse(m, tea.MouseActionMotion, tea.MouseButtonNone, 10, 0)
	at := m.vp.YOffset
	for range 5 {
		m.Update(tickMsg{})
	}
	if m.sel.dragging || m.vp.YOffset != at {
		t.Fatalf("still dragging=%v, scrolled from %d to %d", m.sel.dragging, at, m.vp.YOffset)
	}
}
