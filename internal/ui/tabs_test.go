package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

type tabBackend struct {
	ch     chan claude.Event
	closed bool
}

func (f *tabBackend) Events() <-chan claude.Event                  { return f.ch }
func (f *tabBackend) Send(string) error                            { return nil }
func (f *tabBackend) SendImages(string, []claude.Image) error      { return nil }
func (f *tabBackend) Interrupt() error                             { return nil }
func (f *tabBackend) SetPermissionMode(string) error               { return nil }
func (f *tabBackend) SetModel(string) error                        { return nil }
func (f *tabBackend) Allow(*claude.PermissionRequest, bool) error  { return nil }
func (f *tabBackend) Deny(*claude.PermissionRequest, string) error { return nil }
func (f *tabBackend) Close()                                       { f.closed = true }

// twoSessions returns a model whose session 1 is on screen and session 2 is
// parked, each with its own backend.
func twoSessions(t *testing.T) (*Model, *tabBackend, *tabBackend) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a, b := &tabBackend{ch: make(chan claude.Event)}, &tabBackend{ch: make(chan claude.Event)}
	m.engine, m.client, m.gen, m.genSeq = "claude", a, 1, 2
	m.add(&block{kind: kindUser, text: "first task"})
	m.sessions = append(m.sessions, &session{
		client: b, engine: "claude", gen: 2, tools: map[string]*block{}, follow: true,
		blocks: []*block{{kind: kindUser, text: "second task"}},
	})
	return m, a, b
}

func assistantText(s string) claude.Event {
	return claude.AssistantMessage{Blocks: []claude.ContentBlock{{Type: "text", Text: s}}}
}

func TestBackgroundSessionKeepsWorking(t *testing.T) {
	m, _, _ := twoSessions(t)
	m.openHelp()
	m.Update(eventMsg{gen: 2, ev: &claude.PermissionRequest{ToolName: "Bash"}, ch: nil})
	if m.pop.mode == popNone {
		t.Fatal("an event from another session closed the popup on screen")
	}
	if len(m.blocks) != 1 || m.blocks[0].text != "first task" {
		t.Fatalf("session on screen was changed: %+v", m.blocks)
	}
	if got := m.sessions[1]; len(got.perms) != 1 {
		t.Fatalf("background session did not get the approval request: %+v", got)
	}
	if !strings.Contains(ansi.Strip(m.tabStrip()), "second task !") {
		t.Fatalf("tab strip does not show the waiting session: %q", ansi.Strip(m.tabStrip()))
	}
}

func TestBackgroundTextLandsInItsOwnSession(t *testing.T) {
	m, _, _ := twoSessions(t)
	m.Update(eventMsg{gen: 2, ev: assistantText("done in two"), ch: nil})
	if len(m.blocks) != 1 {
		t.Fatalf("on-screen session got the event: %d blocks", len(m.blocks))
	}
	if n := len(m.sessions[1].blocks); n != 2 || m.sessions[1].blocks[1].text != "done in two" {
		t.Fatalf("parked session blocks: %d", n)
	}
	m.Update(eventMsg{gen: 99, ev: assistantText("stale"), ch: nil})
	if len(m.blocks) != 1 || len(m.sessions[1].blocks) != 2 {
		t.Fatal("an event with an unknown generation was applied")
	}
}

func TestSwitchRestoresEachSession(t *testing.T) {
	m, a, b := twoSessions(t)
	m.input.SetValue("half typed")
	m.switchTo(1)
	if m.cur != 1 || m.client != b || m.gen != 2 || m.blocks[0].text != "second task" {
		t.Fatalf("did not load session 2: cur=%d", m.cur)
	}
	if m.input.Value() != "" {
		t.Fatalf("draft leaked into session 2: %q", m.input.Value())
	}
	m.switchTo(0)
	if m.client != a || m.gen != 1 || m.blocks[0].text != "first task" || m.input.Value() != "half typed" {
		t.Fatalf("did not restore session 1: %q", m.input.Value())
	}
}

func TestCloseSessionStopsItsBackend(t *testing.T) {
	m, a, b := twoSessions(t)
	m.closeSession()
	if !a.closed || b.closed {
		t.Fatalf("closed a=%v b=%v", a.closed, b.closed)
	}
	if len(m.sessions) != 1 || m.client != b || m.blocks[0].text != "second task" {
		t.Fatalf("expected session 2 on screen, got %d sessions", len(m.sessions))
	}
	m.closeSession()
	if b.closed || len(m.sessions) != 1 {
		t.Fatal("the last session must not be closed")
	}
}

func TestQuitClosesEverySession(t *testing.T) {
	m, a, b := twoSessions(t)
	m.quit()
	if !a.closed || !b.closed {
		t.Fatalf("closed a=%v b=%v", a.closed, b.closed)
	}
}

func TestAltNumberSwitchesSession(t *testing.T) {
	m, _, b := twoSessions(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if m.cur != 1 || m.client != b {
		t.Fatalf("alt+2 left session %d on screen", m.cur+1)
	}
	if !strings.Contains(ansi.Strip(m.View()), "second task") {
		t.Fatal("session 2 transcript not shown")
	}
}

func TestTabStripFitsTheWindow(t *testing.T) {
	m, _, _ := twoSessions(t)
	for _, size := range [][2]int{{100, 30}, {60, 12}, {30, 8}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View(), "\n")
		if len(lines) > size[1] {
			t.Fatalf("%dx%d: frame is %d lines", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Fatalf("%dx%d: line is %d wide: %q", size[0], size[1], w, ansi.Strip(l))
			}
		}
	}
	if m.tabStrip() == "" {
		t.Fatal("no tab strip with two sessions")
	}
	m.closeSession()
	if m.tabStrip() != "" || m.tabsHeight() != 0 {
		t.Fatal("tab strip shown with one session")
	}
}

func TestSpendAcrossSessions(t *testing.T) {
	m, _, _ := twoSessions(t)
	if got := m.totalSpend(); got != "" {
		t.Fatalf("nothing has been used yet, got %q", got)
	}
	m.cost = 0.25
	m.sessions[1].tokIn, m.sessions[1].tokOut = 1500, 200
	if got := spendLabel(m.spendOf(1)); got != "1.5k in · 200 out" {
		t.Fatalf("token-only spend = %q", got)
	}
	if got := m.totalSpend(); got != "$0.250" {
		t.Fatalf("total = %q", got)
	}
	m.openSessions()
	var rows []string
	for _, c := range m.pop.all {
		rows = append(rows, c.label+"|"+c.desc)
	}
	all := strings.Join(rows, "\n")
	if !strings.Contains(m.pop.title, "$0.250 in all") || !strings.Contains(all, "$0.250") || !strings.Contains(all, "1.5k in · 200 out") {
		t.Fatalf("sessions picker %q:\n%s", m.pop.title, all)
	}

	m.pop.close()
	m.settings.HideSide = true
	m.layout()
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "$0.250") {
		t.Fatalf("status bar without a sidebar should show the spend: %q", bar)
	}
}

func TestNotifyNamesTheSession(t *testing.T) {
	m, _, _ := twoSessions(t)
	if got := m.notifyText("Claude finished"); got != "session 1: Claude finished" {
		t.Fatalf("got %q", got)
	}
	m.sessions = m.sessions[:1]
	if got := m.notifyText("Claude finished"); got != "Claude finished" {
		t.Fatalf("one session needs no label, got %q", got)
	}
}
