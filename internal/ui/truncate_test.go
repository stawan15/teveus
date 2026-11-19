package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestTruncateKeepsStyledTextIntact(t *testing.T) {
	line := sGreen.Render("✓") + " " + sText.Render("Bash") + " " +
		sDim.Render("sed -n 1,45p internal/agent/live_test.go; sed -n 190,220p internal/ui/model.go")
	got := truncate(line, 30)
	plain := ansi.Strip(got)
	if !strings.HasPrefix(plain, "✓ Bash sed -n") || ansi.StringWidth(got) > 30 {
		t.Fatalf("truncated to %q (width %d)", plain, ansi.StringWidth(got))
	}
	if strings.Contains(got, "\x1b[") && !strings.HasSuffix(strings.TrimSuffix(got, "…"), "m") && !strings.Contains(got, "…") {
		t.Fatalf("escape sequence cut: %q", got)
	}
}

// Terminals draw Thai SARA AM in its own cell, but the width libraries fold it
// into the previous character. The rendered text must measure what it shows.
func TestThaiSaraAmMeasuresLikeTheTerminal(t *testing.T) {
	b := &block{kind: kindUser, text: "ดูหน้าตาเว็บเขาทำง่ายๆเอง"}
	out := ansi.Strip((&renderer{}).render(b, 80))
	if strings.ContainsRune(out, 'ำ') || !strings.Contains(out, "ทํา") {
		t.Fatalf("SARA AM not decomposed: %q", out)
	}
	// One cell per base character, and one for the SARA AA split out of SARA AM.
	if w := ansi.StringWidth(termSafe("ดูหน้าตาเว็บเขาทำง่ายๆเอง")); w != 21 {
		t.Fatalf("width %d, want 21", w)
	}
}

// Rendering must not rewrite the block: what is shown decomposes SARA AM, but
// copying and saving the transcript read the text the model wrote.
func TestRenderLeavesBlockTextAlone(t *testing.T) {
	b := &block{kind: kindAssistant, text: "ทำได้แล้ว"}
	(&renderer{}).render(b, 80)
	if b.text != "ทำได้แล้ว" {
		t.Fatalf("the block was rewritten as %q", b.text)
	}
}

// A streaming reply shows the raw text, so fence markers would appear as bare
// backticks; the finished block renders them as code again.
func TestStreamingHidesFenceMarkers(t *testing.T) {
	got := streamText("Steps:\n\n```go\nq := 1\n```\n")
	if strings.Contains(got, "```") || !strings.Contains(got, "q := 1") {
		t.Fatalf("streamed as %q", got)
	}
}

// A tool result wider than the pane wraps; it must not be cut with an ellipsis.
func TestToolOutputWrapsInsteadOfTruncating(t *testing.T) {
	long := "internal/agent/engine.go:878: the subagent effort lock is gone now"
	lines := wrapLines(long, 20, lipgloss.NewStyle())
	if len(lines) < 3 {
		t.Fatalf("one line survived: %q", lines)
	}
	joined := ansi.Strip(strings.Join(lines, ""))
	if strings.Contains(joined, "…") || strings.ReplaceAll(joined, " ", "") != strings.ReplaceAll(long, " ", "") {
		t.Fatalf("text was cut: %q", joined)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 20 {
			t.Fatalf("line %q is %d wide", l, ansi.StringWidth(l))
		}
	}
}
