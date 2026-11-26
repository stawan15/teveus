package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
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

func TestAntigravityModelEffortAndSessionHistory(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	cwd := t.TempDir()
	m := New(Config{
		Dark: true,
		Claude: claude.Options{
			Cwd: cwd,
		},
		Settings: Settings{Engine: "antigravity", AgyNotice: true, Onboarded: true},
		Agy:      AgyOptions{Binary: "/nonexistent/agy"},
	})
	m.models = []claude.ModelInfo{
		{Value: "gemini-3.8-flash", DisplayName: "Gemini 3.8 Flash", Group: "Antigravity · Gemini", Reasoning: []string{"low", "medium", "high"}},
	}
	// Model selection persists to Settings.AgyModel.
	m.setModel("gemini-3.8-flash")
	if m.settings.AgyModel != "gemini-3.8-flash" || LoadSettings().AgyModel != "gemini-3.8-flash" {
		t.Fatalf("AgyModel not saved: %q / %q", m.settings.AgyModel, LoadSettings().AgyModel)
	}
	// Effort ceiling & effortInUse clamp to the Antigravity model's reasoning range.
	m.settings.Effort = "max"
	if lvl, capped := m.effortInUse(); lvl != "high" || !capped {
		t.Fatalf("effortInUse = (%q, %v), want (high, true)", lvl, capped)
	}
	if capLvl, capped := m.effortCeiling(); capLvl != "high" || !capped {
		t.Fatalf("effortCeiling = (%q, %v), want (high, true)", capLvl, capped)
	}
	// Saved Antigravity sessions appear in /resume and /search.
	if err := agent.SaveSession(sessionDir(), agent.Session{
		ID: "agy-sess-1", Engine: "antigravity", Cwd: cwd, Model: "gemini-3.8-flash",
		Title: "Fix slow query", Updated: time.Now(),
		History: []agent.Message{
			{Role: "user", Text: "Fix slow query on orders table"},
			{Role: "assistant", Text: "Added an index on orders(created_at)."},
		},
	}); err != nil {
		t.Fatal(err)
	}
	all := listAllSessions(cwd)
	if len(all) != 1 || all[0].engine != "antigravity" || all[0].id != "agy-sess-1" {
		t.Fatalf("listAllSessions = %+v", all)
	}
	if hit, ok := searchSession(all[0], "orders"); !ok || hit.n < 2 {
		t.Fatalf("searchSession = (%+v, %v)", hit, ok)
	}
	m.openResumePicker()
	if len(m.pop.list) != 1 || m.pop.list[0].group != "Google (Antigravity CLI)" {
		t.Fatalf("resume picker = %+v", m.pop.list)
	}
}

func TestAntigravityContextUsageRendersSidebarBar(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{
		Dark:     true,
		Settings: Settings{Engine: "antigravity", AgyNotice: true, Onboarded: true},
		Agy:      AgyOptions{Binary: "/nonexistent/agy"},
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.handleEvent(claude.ContextUsage{Used: 9863, Size: 1048576, Tier: "Google AI Pro"})
	side := ansi.Strip(m.sidebar(30))
	for _, want := range []string{"USAGE", "Google AI Pro", "ctx", "1%", "9.9k"} {
		if !strings.Contains(side, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, side)
		}
	}
}
