package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

func TestClaudeCodeWaitsForConfirmation(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	// -engine claude: connecting at startup, so the notice shows then.
	m := New(Config{Dark: true, Engine: "claude", Claude: claude.Options{Binary: "/nonexistent/claude"}})
	m.settings.Onboarded = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.start(m.cfg.Claude)
	if m.client != nil || m.pop.title != "Use Claude Code?" {
		t.Fatalf("Claude Code started without asking (popup %q)", m.pop.title)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"your own Claude Code", "never sees your login", "Anthropic's terms", "not made or endorsed by Anthropic", "Continue with Claude Code", "take a moment to read", "Use my own API keys instead"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Fatalf("notice lacks %q:\n%s", want, view)
		}
	}
	// "Not now" leaves it off; a message typed meanwhile is kept and asks again.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.input.SetValue("hello")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.client != nil || m.input.Value() != "hello" || m.pop.title != "Use Claude Code?" {
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
	if m.settings.ClaudeNotice || m.pop.title != "Use Claude Code?" {
		t.Fatal("accepted before the notice could be read")
	}
	m.pop.holdEnd = time.Now() // time passes
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.settings.ClaudeNotice || !LoadSettings().ClaudeNotice {
		t.Fatal("confirmation not saved")
	}
	m2 := New(Config{Dark: true, Settings: LoadSettings(), Claude: claude.Options{Binary: "/nonexistent/claude"}})
	m2.settings.Onboarded = true
	if m2.gateClaude(m2.cfg.Claude) {
		t.Fatal("asked again after agreeing")
	}
}

func TestSetupAsksInsteadOfStartup(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Onboard: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.start(m.cfg.Claude)
	if m.client != nil || m.pop.open() {
		t.Fatal("first-run setup should ask, not startup")
	}
	// Choosing Claude Code in setup's last step asks first.
	m.onboardConnect()
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // "Claude Code" is setup's first choice
	if m.pop.title != "Use Claude Code?" {
		t.Fatalf("setup didn't ask: %q", m.pop.title)
	}
}

func TestFirstRunIsDisconnectedAndConnectingClaudeAsks(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.settings.Onboarded = true
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.start(m.cfg.Claude)
	view := ansi.Strip(m.View())
	if m.client != nil || m.pop.open() || !strings.Contains(view, "Connect an AI to start") || !strings.Contains(view, "not connected") {
		t.Fatalf("first run should be quietly disconnected:\n%s", view)
	}
	// A message before connecting stays in the input and opens /login.
	m.input.SetValue("fix the bug")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.input.Value() != "fix the bug" || m.pop.title != "Connect a provider" {
		t.Fatalf("input=%q popup=%q", m.input.Value(), m.pop.title)
	}
	// Choosing Claude Code is when the notice appears.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.setEngine("claude")
	if m.engine != "" || m.pop.title != "Use Claude Code?" {
		t.Fatalf("engine=%q popup=%q", m.engine, m.pop.title)
	}
}
