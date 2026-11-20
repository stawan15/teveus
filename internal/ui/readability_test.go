package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func replyLines(t *testing.T, md string, width int) []string {
	t.Helper()
	out := (&renderer{cwd: "/tmp"}).markdown(md, width)
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		lines = append(lines, strings.TrimRight(ansi.Strip(l), " "))
	}
	return lines
}

// Heading rank has to be readable without reading the words: an h2 is set in
// capitals, and the "## " glamour printed in front of it is gone.
func TestHeadingRankIsVisible(t *testing.T) {
	lines := replyLines(t, "# Title\n\n## A Section\n\ntext\n\n### A Detail\n\nmore\n", 60)
	var h1, h2, h3 string
	for _, l := range lines {
		switch {
		case l == " Title" || strings.HasPrefix(l, "Title"):
			h1 = l
		case strings.Contains(l, "SECTION"):
			h2 = l
		case strings.Contains(l, "A Detail"):
			h3 = l
		}
	}
	if h1 == "" || h2 == "" || h3 == "" {
		t.Fatalf("headings missing: %q %q %q\n%v", h1, h2, h3, lines)
	}
	if strings.Contains(h2, "##") {
		t.Errorf("the heading still carries its markdown mark: %q", h2)
	}
	if h2 != strings.ToUpper(h2) {
		t.Errorf("an h2 isn't set apart from body text: %q", h2)
	}
}

// Nested points have to sit inside their parent, or a sub-point reads as
// another top-level one.
func TestNestedListIsIndented(t *testing.T) {
	lines := replyLines(t, "- top level\n\n  - nested point\n", 60)
	var top, nested string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "• top"):
			top = l
		case strings.Contains(l, "nested point"):
			nested = l
		}
	}
	if top == "" || nested == "" {
		t.Fatalf("list missing:\n%v", lines)
	}
	if lead(nested) <= lead(top) {
		t.Errorf("nested point is not indented: %q vs %q", nested, top)
	}
}

// A wrapped bullet used to sit one space from the next bullet, so a few
// multi-line points read as one paragraph. The gap marks where an item ends,
// and a list of single-line bullets stays dense.
func TestWrappedBulletsAreSeparated(t *testing.T) {
	wrapped := replyLines(t, "- a bullet long enough that it has to wrap onto a second line\n- another one\n", 40)
	gapped := false
	for i, l := range wrapped {
		if strings.HasPrefix(l, "•") && i > 0 && wrapped[i-1] == "" {
			gapped = true
		}
	}
	if !gapped {
		t.Errorf("wrapped items are not separated:\n%s", strings.Join(wrapped, "\n"))
	}

	dense := replyLines(t, "- one\n- two\n- three\n", 60)
	if blanks := strings.Count(strings.Join(dense, "\n"), "\n\n"); blanks != 0 {
		t.Errorf("single-line bullets got a gap:\n%s", strings.Join(dense, "\n"))
	}
}

// A bullet inside a fenced block is code, not a list, and must not be spaced.
func TestListGapLeavesCodeAlone(t *testing.T) {
	lines := replyLines(t, "```\n- flag one\n- flag two\n```\n", 60)
	for i, l := range lines {
		if i > 0 && l == "" && i < len(lines)-1 {
			t.Errorf("blank line added inside a code block:\n%s", strings.Join(lines, "\n"))
		}
	}
}
