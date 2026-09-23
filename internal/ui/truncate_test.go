package ui

import (
	"strings"
	"testing"

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
