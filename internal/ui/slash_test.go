package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func slashPopup(t *testing.T, input string) []choice {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.commands = []claude.Command{{Name: "review", Description: "Review a pull request"}}
	m.input.SetValue(input)
	m.afterInput()
	return m.pop.list
}

func names(cs []choice) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		out[c.label] = true
	}
	return out
}

func TestSlashListsOnlyCoreCommandsUntilYouType(t *testing.T) {
	got := names(slashPopup(t, "/"))
	if len(got) != len(coreCmds) {
		t.Fatalf("got %d commands, want %d: %v", len(got), len(coreCmds), got)
	}
	for c := range coreCmds {
		if !got["/"+c] {
			t.Errorf("/%s missing", c)
		}
	}
}

func TestSlashSearchFindsHiddenCommands(t *testing.T) {
	for in, want := range map[string]string{"/pur": "/purge", "/mou": "/mouse", "/rev": "/review"} {
		if !names(slashPopup(t, in))[want] {
			t.Errorf("%s did not find %s", in, want)
		}
	}
}

// A skill dropped into the project's own ".agent" layout is a slash command
// straight away: no setup, no restart, and Claude Code's ".claude" list isn't
// needed for it to appear.
func TestOwnLayoutShowsUpInTheSlashMenu(t *testing.T) {
	dir := t.TempDir()
	m := newModelIn(t, dir)
	writeSkill(t, dir, "review", "Read every hunk twice.")
	m.input.SetValue("/rev")
	m.afterInput()
	if !names(m.pop.list)["/review"] {
		t.Fatalf("menu = %v", names(m.pop.list))
	}
}

// Claude Code can't read ".agent", so the app fills those commands in itself
// and the transcript still shows what the user typed.
func TestOwnLayoutCommandIsExpandedForClaudeCode(t *testing.T) {
	dir := t.TempDir()
	m := newModelIn(t, dir)
	rec := &sendRecorder{}
	m.client, m.engine = rec, "claude"
	writeSkill(t, dir, "review", "Read every hunk twice.")
	m.submit("/review only the parser")
	if len(rec.sent) != 1 || rec.sent[0] != "Read every hunk twice.\n\nARGUMENTS: only the parser" {
		t.Fatalf("sent %q", rec.sent)
	}
	if last := m.blocks[len(m.blocks)-1]; last.text != "/review only the parser" {
		t.Fatalf("transcript shows %q", last.text)
	}
}

func newModelIn(t *testing.T, dir string) *Model {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true, Claude: claude.Options{Cwd: dir}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func writeSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, ".agent", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ndescription: "+name+"\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type sendRecorder struct {
	claude.Backend
	sent []string
}

func (s *sendRecorder) Send(text string) error { s.sent = append(s.sent, text); return nil }
