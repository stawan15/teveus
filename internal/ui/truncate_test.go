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
