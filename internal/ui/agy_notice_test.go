package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestAntigravityWaitsForConfirmation(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	// -engine antigravity: connecting at startup, so the notice shows then.
	m := New(Config{Dark: true, Engine: "antigravity", Agy: AgyOptions{Binary: "/nonexistent/agy"}})
	m.settings.Onboarded = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.start(m.cfg.Claude)
	if m.client != nil || m.pop.title != "Use the Antigravity CLI?" {
		t.Fatalf("Antigravity started without asking (popup %q)", m.pop.title)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"your own Antigravity CLI", "never sees your login", "Google's terms", "not made or endorsed by Google", "Continue with Antigravity CLI", "take a moment to read", "Use my own API keys instead"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Fatalf("notice lacks %q:\n%s", want, view)
		}
	}
	// "Not now" leaves it off; a message typed meanwhile is kept and asks again.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.input.SetValue("hello")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.client != nil || m.input.Value() != "hello" || m.pop.title != "Use the Antigravity CLI?" {
		t.Fatalf("message lost or not asked again: input=%q popup=%q", m.input.Value(), m.pop.title)
	}
	// The cursor starts on "Not now", and "Continue" can't be picked until
	// the notice has had time to be read.
	if c, _ := m.pop.current(); c.label != "Not now" {
		t.Fatalf("starts on %q", c.label)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.settings.AgyNotice || m.pop.title != "Use the Antigravity CLI?" {
		t.Fatal("accepted before the notice could be read")
	}
	m.pop.holdEnd = time.Now() // time passes
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.settings.AgyNotice || !LoadSettings().AgyNotice {
		t.Fatal("confirmation not saved")
	}
	m2 := New(Config{Dark: true, Settings: LoadSettings(), Agy: AgyOptions{Binary: "/nonexistent/agy"}})
	m2.settings.Onboarded = true
	if m2.gateAgy(m2.cfg.Claude) {
		t.Fatal("asked again after agreeing")
	}
}

func TestConnectingAntigravityAsksAndLabelsProperly(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Agy: AgyOptions{Binary: "/nonexistent/agy"}})
	m.settings.Onboarded = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	// Switching via /engine agy asks before connecting.
	m.setEngine("antigravity")
	if m.engine != "" || m.pop.title != "Use the Antigravity CLI?" {
		t.Fatalf("engine=%q popup=%q", m.engine, m.pop.title)
	}
	// Once confirmed, header chip and agent name reflect Antigravity.
	m.engine = "antigravity"
	if got := m.agentName(); got != "Antigravity" {
		t.Fatalf("agentName() = %q, want Antigravity", got)
	}
	m.model = "gemini-2.5-pro"
	if got := m.agentName(); got != "gemini-2.5-pro" {
		t.Fatalf("agentName() with model = %q", got)
	}
	if !strings.Contains(ansi.Strip(m.header()), "Agy") {
		t.Fatalf("header missing Agy chip: %q", ansi.Strip(m.header()))
	}
}
