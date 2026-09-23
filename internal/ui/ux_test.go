package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func press(m *Model, s string) {
	switch s {
	case "enter":
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	case "down":
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	case "esc":
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	default:
		for _, r := range s {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func TestOnboardingFlow(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Onboard: true})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m.Update(claudeAuthMsg{loggedIn: true, email: "me@example.com"})
	if !strings.Contains(m.pop.title, "1/3") {
		t.Fatalf("setup did not start: %q", m.pop.title)
	}
	if os.Getenv("SNAP") != "" {
		t.Log("step 1:\n" + plain(m.View()))
	}
	press(m, "down") // Pro
	press(m, "enter")
	if m.settings.Level != "pro" || !strings.Contains(m.pop.title, "2/3") {
		t.Fatalf("level=%q title=%q", m.settings.Level, m.pop.title)
	}
	press(m, "down") // preview next theme
	previewed := theme.Name
	press(m, "enter")
	if theme.Name != previewed || m.settings.Theme != previewed || !strings.Contains(m.pop.title, "3/3") {
		t.Fatalf("theme=%q saved=%q title=%q", theme.Name, m.settings.Theme, m.pop.title)
	}
	if os.Getenv("SNAP") != "" {
		t.Log("step 3:\n" + plain(m.View()))
	}
	press(m, "down")
	press(m, "down") // Decide later
	press(m, "enter")
	if !m.settings.Onboarded || m.pop.open() {
		t.Fatalf("onboarded=%v popup=%v", m.settings.Onboarded, m.pop.open())
	}
	// never again
	m2 := New(Config{Dark: true, Onboard: true, Settings: LoadSettings()})
	m2.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m2.Update(claudeAuthMsg{})
	if m2.pop.open() {
		t.Fatal("setup shown twice")
	}
	applyTheme(themes[0])
}

func TestEscSkipsOnboarding(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Onboard: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(claudeAuthMsg{})
	press(m, "esc")
	if !m.settings.Onboarded || m.pop.open() {
		t.Fatal("esc should skip setup for good")
	}
}

func TestStartersHelpAndSettings(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	if os.Getenv("SNAP") != "" {
		t.Log("welcome:\n" + plain(m.View()))
	}
	press(m, "2")
	if m.input.Value() != starters[1] {
		t.Fatalf("starter 2 = %q", m.input.Value())
	}
	m.input.Reset()

	press(m, "?")
	if m.pop.mode != popPicker || !strings.Contains(m.pop.title, "Keyboard") {
		t.Fatalf("? did not open help: %q", m.pop.title)
	}
	press(m, "undo")
	if c, _ := m.pop.current(); c.label != "/undo" {
		t.Fatalf("help search found %q", c.label)
	}
	press(m, "esc")

	press(m, "/settings")
	press(m, "enter")
	if m.pop.title != "Settings" {
		t.Fatalf("settings title %q", m.pop.title)
	}
	if os.Getenv("SNAP") != "" {
		t.Log("settings:\n" + plain(m.View()))
	}
	// toggle "Reduce motion" (3rd row) and stay in the list
	press(m, "down")
	press(m, "down")
	press(m, "enter")
	if !m.settings.ReduceMotion || m.pop.title != "Settings" || m.pop.sel != 2 {
		t.Fatalf("reduce motion=%v title=%q sel=%d", m.settings.ReduceMotion, m.pop.title, m.pop.sel)
	}
	m.Update(tickMsg{})
	if m.r.spin != "●" {
		t.Fatalf("spinner still animates: %q", m.r.spin)
	}
}

func TestNarrowWindowsDoNotOverflow(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	for _, w := range []int{40, 50, 70} {
		m := New(Config{Dark: true})
		m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		for i, l := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d line %d overflows: %d %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
}
