package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

func TestSnippet(t *testing.T) {
	long := strings.Repeat("word ", 40) + "The Needle is here" + strings.Repeat(" tail", 40)
	got, ok := snippet(long, "needle")
	if !ok || !strings.Contains(got, "The Needle is here") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("snippet = %q, %v", got, ok)
	}
	if len([]rune(got)) > 100 {
		t.Fatalf("snippet too long: %d", len([]rune(got)))
	}
	if _, ok := snippet("nothing here", "needle"); ok {
		t.Fatal("matched text that isn't there")
	}
	// Multi-line text and non-ASCII must not break the offsets.
	if got, ok := snippet("İstanbul\n\nÜber   alles: ÉCOLE", "école"); !ok || !strings.Contains(got, "ÉCOLE") {
		t.Fatalf("snippet = %q, %v", got, ok)
	}
}

func TestSearchFindsEarlierAndCurrentConversations(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	agent.SaveSession(sessionDir(), agent.Session{ID: "old1", Cwd: cwd, Title: "database work", Updated: time.Now(),
		History: []agent.Message{
			{Role: "user", Text: "please migrate the Postgres schema"},
			{Role: "assistant", Text: "Migrated the postgres tables."},
			{Role: "tool", Result: "postgres output that must not count"},
		}})
	agent.SaveSession(sessionDir(), agent.Session{ID: "old2", Cwd: cwd, Title: "unrelated", Updated: time.Now(),
		History: []agent.Message{{Role: "user", Text: "fix the css"}}})
	agent.SaveSession(sessionDir(), agent.Session{ID: "other", Cwd: t.TempDir(), Title: "another folder", Updated: time.Now(),
		History: []agent.Message{{Role: "user", Text: "postgres elsewhere"}}})

	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd = cwd
	m.add(&block{kind: kindUser, text: "is postgres running?"})
	m.add(&block{kind: kindAssistant, text: "Yes, postgres is up."})
	m.add(&block{kind: kindUser, text: "thanks"})
	m.refresh()

	cmd := m.search("Postgres")
	msg, ok := cmd().(searchMsg)
	if !ok || len(msg.hits) != 1 || msg.hits[0].s.id != "old1" || msg.hits[0].n != 2 {
		t.Fatalf("earlier hits = %+v", msg.hits)
	}
	m.Update(msg)
	if !m.pop.open() || len(m.pop.all) != 3 {
		t.Fatalf("picker has %d rows, want 2 here + 1 earlier", len(m.pop.all))
	}
	if g := m.pop.all[0].group; g != "This conversation" || m.pop.all[2].group != "Earlier conversations" {
		t.Fatalf("groups: %q … %q", g, m.pop.all[2].group)
	}

	// An empty search asks for a query instead of listing everything.
	m.pop.close()
	if m.search(""); m.input.Value() != "/search " {
		t.Fatalf("input = %q", m.input.Value())
	}
}

func TestSearchNoMatch(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(searchMsg{query: "zzz"})
	if m.pop.open() || !strings.Contains(m.notice, "no matches") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestSearchSkipsUnreadableClaudeSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"find the Widget factory"}}
not json
{"type":"assistant","message":{"content":[{"type":"text","text":"the widget factory is in a.go"},{"type":"tool_use","name":"Read"}]}}
`), 0o644)
	h, ok := searchSession(savedSession{engine: "claude", path: path}, "widget")
	if !ok || h.n != 2 {
		t.Fatalf("hit = %+v, %v", h, ok)
	}
}
