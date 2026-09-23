package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestEffortSlider(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.submit("/effort")
	if m.pop.mode != popSlider || m.pop.sel != 0 {
		t.Fatalf("slider not open at auto: %v %d", m.pop.mode, m.pop.sel)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")}) // jump to xhigh
	view := ansi.Strip(m.View())
	for _, want := range []string{"Reasoning effort", "auto", "low", "medium", "high", "xhigh", "max", "faster · cheaper", "smarter · slower", "sweet spot for coding"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.effort() != "xhigh" || LoadSettings().Effort != "xhigh" {
		t.Fatalf("effort = %s", m.effort())
	}
	// The Claude engine gets it as --effort when Claude Code starts.
	if opts := m.startOptions(m.cfg.Claude); opts.Effort != "xhigh" {
		t.Fatalf("start options effort = %q", opts.Effort)
	}
	shown := false
	for _, l := range strings.Split(ansi.Strip(m.sidebar(20)), "\n") {
		f := strings.Fields(strings.Trim(l, "│ "))
		shown = shown || (len(f) == 2 && f[0] == "effort" && f[1] == "xhigh")
	}
	if !shown {
		t.Fatal("sidebar doesn't show the effort")
	}
	m.submit("/effort auto")
	if m.settings.Effort != "" || m.startOptions(m.cfg.Claude).Effort != "" {
		t.Fatal("auto should clear it")
	}
}
