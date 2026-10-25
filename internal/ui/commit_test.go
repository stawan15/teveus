package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

// dirty makes gitRepo's tree dirty again and returns its path.
func dirty(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	if _, err := git(dir, "commit", "-qm", msg); err != nil {
		t.Fatal(err)
	}
}

func TestCommitChecksForWorkFirst(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	dir := dirty(t)
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd = dir
	m.engine, m.client = "api", &tabBackend{ch: make(chan claude.Event)}

	// With changes, it hands off to the model rather than committing behind
	// the user's back.
	cmd := m.commit("")
	if cmd == nil {
		t.Fatal("no command")
	}
	msg := cmd()
	if _, ok := msg.(commitStartMsg); !ok {
		t.Fatalf("message = %#v, want commitStartMsg", msg)
	}
}

func TestCommitSaysWhenThereIsNothingToDo(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	dir := dirty(t)
	commitAll(t, dir, "done") // clean tree again
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd = dir

	msg := m.commit("")()
	d, ok := msg.(diffMsg)
	if !ok {
		t.Fatalf("message = %#v, want a plain answer", msg)
	}
	if !strings.Contains(string(d), "Nothing to commit") {
		t.Fatalf("answer = %q", d)
	}
}

func TestCommitOutsideARepo(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd = t.TempDir() // no .git

	msg := m.commit("")()
	d, ok := msg.(diffMsg)
	if !ok || !strings.Contains(string(d), "isn't a git repository") {
		t.Fatalf("message = %#v", msg)
	}
}

func TestCommitPromptIsSentButTheTypedTextIsShown(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	dir := dirty(t)
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd = dir
	b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event)}}
	m.engine, m.client = "api", b

	m.commitArg = "just the parser"
	m.startCommit()

	if len(b.sent) != 1 {
		t.Fatalf("sent %d messages", len(b.sent))
	}
	sent := b.sent[0]
	if !strings.Contains(sent, "git status --short") || !strings.Contains(sent, "just the parser") {
		t.Fatalf("prompt = %q", sent)
	}
	if !strings.Contains(sent, "Don't push") {
		t.Fatal("the prompt doesn't say not to push")
	}

	// The transcript shows what was typed, not the instructions.
	view := ansi.Strip(strings.Join(func() []string {
		m.layout()
		m.refresh()
		return m.lines
	}(), "\n"))
	if !strings.Contains(view, "/commit just the parser") {
		t.Fatalf("the typed command isn't what the transcript shows:\n%s", view)
	}
	if strings.Contains(view, "git status --short") {
		t.Fatalf("the prompt leaked into the transcript:\n%s", view)
	}
}

func TestCommitRefusesWhileBusy(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.busy = true

	if cmd := m.commit(""); cmd != nil {
		t.Fatal("/commit started a second turn while one was running")
	}
	if !strings.Contains(ansi.Strip(m.statusBar()), "finish") {
		t.Fatalf("no explanation: %s", ansi.Strip(m.statusBar()))
	}
}

// captureBackend records what the app sends, for /commit's prompt.
type captureBackend struct {
	tabBackend
	sent []string
}

func (b *captureBackend) Send(text string) error {
	b.sent = append(b.sent, text)
	return nil
}
