package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A session can work in its own git worktree, so several sessions editing the
// same repository don't overwrite each other's files. The worktree branches
// from HEAD into <repo>/.teveus/worktrees/<id> (kept out of git status through
// .git/info/exclude) and goes away when its session closes, unless it holds
// uncommitted work.

type worktree struct{ root, path, branch string }

type worktreeMsg struct {
	wt        worktree
	cwd, name string // the session's folder inside the worktree, and its branch
	err       error
}

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil && s != "" {
		err = fmt.Errorf("%s", s)
	}
	return s, err
}

// newWorktree creates the worktree in the background, then opens a session in it.
func (m *Model) newWorktree() tea.Cmd {
	switch {
	case m.engine == "":
		m.note("connect an AI first: /login", false)
		return nil
	case len(m.sessions) >= maxSessions:
		m.note(fmt.Sprintf("at most %d sessions: close one first", maxSessions), false)
		return nil
	}
	cwd := m.home
	m.note("creating a worktree…", true)
	return func() tea.Msg {
		root, err := git(cwd, "rev-parse", "--show-toplevel")
		if err != nil {
			return worktreeMsg{err: fmt.Errorf("worktrees need a git repository")}
		}
		id := make([]byte, 3)
		rand.Read(id)
		name := "teveus/" + hex.EncodeToString(id)
		path := filepath.Join(root, ".teveus", "worktrees", hex.EncodeToString(id))
		if ex, err := git(root, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude"); err == nil {
			excludeOnce(ex, "/.teveus/")
		}
		if _, err := git(root, "worktree", "add", "-b", name, path); err != nil {
			return worktreeMsg{err: err}
		}
		if real, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = real // git reports the repository's real path
		}
		rel, _ := filepath.Rel(root, cwd)
		return worktreeMsg{wt: worktree{root: root, path: path, branch: name}, cwd: filepath.Join(path, rel), name: name}
	}
}

// excludeOnce adds a line to a git exclude file if it isn't there.
func excludeOnce(file, line string) {
	b, _ := os.ReadFile(file)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return
		}
	}
	os.MkdirAll(filepath.Dir(file), 0o755)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		f.WriteString("\n")
	}
	f.WriteString(line + "\n")
}

// removeWorktree deletes a finished session's worktree when nothing in it
// would be lost, and says what it did.
func removeWorktree(wt worktree) string {
	if out, err := git(wt.path, "status", "--porcelain"); err != nil || out != "" {
		return "kept the worktree " + wt.path + " (it has uncommitted changes)"
	}
	if _, err := git(wt.root, "worktree", "remove", wt.path); err != nil {
		return "kept the worktree " + wt.path + ": " + err.Error()
	}
	// -d refuses a branch with commits that aren't merged, which keeps them.
	if _, err := git(wt.root, "branch", "-d", wt.branch); err != nil {
		return "removed the worktree; kept branch " + wt.branch + ", which has commits"
	}
	return "removed the worktree"
}
