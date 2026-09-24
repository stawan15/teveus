package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := git(".", "--version"); err != nil {
		t.Skip("git not installed")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"}} {
		if _, err := git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git(dir, "add", ".")
	if _, err := git(dir, "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWorktreeSession(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.cwd, m.home = repo, repo
	m.engine = "claude" // not confirmed, so no real backend starts
	m.client = &tabBackend{ch: make(chan claude.Event)}

	msg, ok := m.newWorktree()().(worktreeMsg)
	if !ok || msg.err != nil {
		t.Fatalf("worktree: %+v", msg)
	}
	if !strings.HasPrefix(msg.name, "teveus/") || !strings.HasPrefix(msg.wt.path, filepath.Join(repo, ".teveus", "worktrees")) {
		t.Fatalf("worktree = %+v", msg)
	}
	if _, err := os.Stat(filepath.Join(msg.wt.path, "a.txt")); err != nil {
		t.Fatal("the worktree has no checkout")
	}
	if out, _ := git(repo, "status", "--porcelain"); out != "" {
		t.Fatalf("the worktree shows up in the repository's status: %q", out)
	}

	// The session on screen switches to the worktree; the first keeps the project.
	m.Update(msg)
	if len(m.sessions) != 2 || m.cwd != msg.cwd || m.branch != msg.name || m.sessions[1].wt == nil {
		t.Fatalf("sessions=%d cwd=%q branch=%q", len(m.sessions), m.cwd, m.branch)
	}
	m.switchTo(0)
	if m.cwd != repo {
		t.Fatalf("session 1 lost its folder: %q", m.cwd)
	}
	m.switchTo(1)

	// Clean: closing removes the worktree and its empty branch.
	m.closeSession()
	if _, err := os.Stat(msg.wt.path); err == nil {
		t.Fatal("the clean worktree was kept")
	}
	if out, _ := git(repo, "branch", "--list", msg.name); out != "" {
		t.Fatalf("the branch was kept: %q", out)
	}
	if m.cwd != repo {
		t.Fatalf("after closing, cwd = %q", m.cwd)
	}
}

func TestWorktreeWithChangesIsKept(t *testing.T) {
	repo := gitRepo(t)
	root, _ := git(repo, "rev-parse", "--show-toplevel")
	path := filepath.Join(root, ".teveus", "wt")
	if _, err := git(root, "worktree", "add", "-b", "teveus/x", path); err != nil {
		t.Fatal(err)
	}
	wt := worktree{root: root, path: path, branch: "teveus/x"}
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("work"), 0o644)
	if got := removeWorktree(wt); !strings.Contains(got, "kept") {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(path, "new.txt")); err != nil {
		t.Fatal("uncommitted work was deleted")
	}

	// With the work committed, the worktree goes but the branch stays.
	git(path, "add", ".")
	git(path, "commit", "-qm", "work")
	if got := removeWorktree(wt); !strings.Contains(got, "kept branch") {
		t.Fatalf("got %q", got)
	}
	if out, _ := git(root, "branch", "--list", "teveus/x"); out == "" {
		t.Fatal("the branch with commits was deleted")
	}
}

func TestExcludeOnce(t *testing.T) {
	f := filepath.Join(t.TempDir(), "info", "exclude")
	excludeOnce(f, "/.teveus/")
	excludeOnce(f, "/.teveus/")
	if b, _ := os.ReadFile(f); string(b) != "/.teveus/\n" {
		t.Fatalf("exclude = %q", b)
	}
}
