package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

type harness struct {
	t    *testing.T
	m    *Model
	msgs chan tea.Msg
}

func newHarness(t *testing.T, cfg Config) *harness {
	h := &harness{t: t, m: New(cfg), msgs: make(chan tea.Msg, 1024)}
	h.run(h.m.Init())
	h.step(tea.WindowSizeMsg{Width: 110, Height: 34})
	return h
}

func (h *harness) run(c tea.Cmd) {
	if c == nil {
		return
	}
	go func() {
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				h.run(c)
			}
			return
		}
		if msg != nil {
			h.msgs <- msg
		}
	}()
}

func (h *harness) step(msg tea.Msg) { _, c := h.m.Update(msg); h.run(c) }

func (h *harness) typ(s string) {
	for _, r := range s {
		if r == ' ' {
			h.step(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		} else {
			h.step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func (h *harness) enter() { h.step(tea.KeyMsg{Type: tea.KeyEnter}) }

func (h *harness) until(what string, cond func() bool) {
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case msg := <-h.msgs:
			h.step(msg)
		case <-deadline:
			h.t.Fatalf("timeout waiting for %s:\n%s", what, plain(h.m.View()))
		}
	}
}

// chatServer answers each chat request with the next scripted reply and
// records requests.
type chatServer struct {
	mu       sync.Mutex
	replies  []string
	requests []string
}

func (c *chatServer) start(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			io.WriteString(w, `{"data":[{"id":"m1"}]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.requests = append(c.requests, string(b))
		reply := `{"choices":[{"delta":{"content":"ok"}}]}`
		if len(c.replies) > 0 {
			reply, c.replies = c.replies[0], c.replies[1:]
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range strings.Split(reply, "\n") {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
}

func writeCall(path, content string) string {
	args, _ := json.Marshal(fmt.Sprintf(`{"file_path":%q,"content":%q}`, path, content))
	return `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w1","function":{"name":"Write","arguments":` + string(args) + `}}]},"finish_reason":"tool_calls"}]}`
}

func apiSetup(t *testing.T, cs *chatServer) (string, string) {
	cfg := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", cfg)
	srv := cs.start(t)
	t.Cleanup(srv.Close)
	agent.NewStore(cfg).Save("custom", agent.Credential{BaseURL: srv.URL})
	return cfg, t.TempDir()
}

func TestSlashQSuggestsExit(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if c, _ := m.pop.current(); c.value != "exit" {
		t.Fatalf("/q suggests %q first", c.value)
	}
	if _, ok := m.localCommand("quit", ""); !ok {
		t.Fatal("/quit not handled")
	}
}

func TestUndoResumeAndShell(t *testing.T) {
	cs := &chatServer{}
	_, dir := apiSetup(t, cs)
	target := filepath.Join(dir, "notes.txt")
	os.WriteFile(target, []byte("original\n"), 0o644)
	cs.replies = []string{
		// Write needs a prior Read of an existing file; use a new file too.
		writeCall(filepath.Join(dir, "new.txt"), "created\n"),
		`{"choices":[{"delta":{"content":"Made new.txt."}}]}`,
	}

	h := newHarness(t, Config{Claude: claude.Options{Cwd: dir, Model: "custom/m1", PermissionMode: "acceptEdits"}, Dark: true, Engine: "api"})
	h.until("ready", func() bool { return h.m.sessionID != "" && len(h.m.models) > 0 })
	h.typ("make new.txt")
	h.enter()
	h.until("turn", func() bool { return !h.m.busy && len(h.m.blocks) >= 3 })
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal("new.txt not written")
	}

	// !command: output shown and added to the conversation
	h.typ("!echo shell-works")
	h.enter()
	h.until("shell", func() bool {
		for _, b := range h.m.blocks {
			if b.kind == kindCmdOut && strings.Contains(b.text, "shell-works") {
				return true
			}
		}
		return false
	})

	// /undo removes new.txt, drops the exchange and puts the prompt back
	h.typ("/undo")
	h.enter()
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("undo did not remove new.txt")
	}
	if h.m.input.Value() != "make new.txt" {
		t.Fatalf("prompt not restored: %q", h.m.input.Value())
	}
	h.m.input.Reset()

	// ask again, then resume the saved session in a fresh model
	h.typ("second question")
	h.enter()
	h.until("second turn", func() bool {
		return !h.m.busy && strings.Contains(plain(h.m.View()), "second question") && len(cs.requests) >= 3
	})
	id := h.m.sessionID
	h.m.client.Close()

	h2 := newHarness(t, Config{Claude: claude.Options{Cwd: dir, Model: "custom/m1"}, Dark: true, Engine: "api"})
	h2.until("ready", func() bool { return h2.m.sessionID != "" })
	h2.typ("/resume")
	h2.enter()
	if h2.m.pop.mode != popPicker || len(h2.m.pop.list) == 0 {
		t.Fatalf("no resume picker:\n%s", plain(h2.m.View()))
	}
	h2.enter()
	if h2.m.sessionID != id {
		t.Fatalf("resumed %q, want %q", h2.m.sessionID, id)
	}
	view := plain(h2.m.View())
	if !strings.Contains(view, "second question") || strings.Contains(view, "make new.txt") {
		t.Fatalf("replayed transcript wrong:\n%s", view)
	}
	h2.typ("third")
	h2.enter()
	h2.until("third turn", func() bool { return !h2.m.busy && len(cs.requests) >= 4 })
	last := cs.requests[len(cs.requests)-1]
	// /undo rewound everything since the undone turn began, including the
	// shell output that followed it (also removed from the transcript).
	if !strings.Contains(last, "second question") || strings.Contains(last, "shell-works") ||
		strings.Contains(last, "make new.txt") || strings.Contains(last, "null") {
		t.Fatalf("resumed context wrong: %s", last)
	}
	h2.m.client.Close()
}

func TestDiffAndClaudeSessionReplay(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "proj")
	os.MkdirAll(dir, 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Run()
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644)

	// a Claude Code session file for this folder
	pdir := claudeProjectDir(dir)
	os.MkdirAll(pdir, 0o755)
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":"fix the login bug"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"a.txt"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent noise"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Fixed it."}]}}`,
	}
	os.WriteFile(filepath.Join(pdir, "sess-1.jsonl"), []byte(strings.Join(lines, "\n")), 0o600)

	ss := listClaudeSessions(dir)
	if len(ss) != 1 || ss[0].title != "fix the login bug" || ss[0].model != "claude-opus-5-5" {
		t.Fatalf("sessions = %+v", ss)
	}
	m := New(Config{Claude: claude.Options{Cwd: dir}, Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.replayClaude(ss[0].path)
	m.refresh()
	view := plain(m.View())
	for _, want := range []string{"fix the login bug", "Looking.", "Bash", "Fixed it."} {
		if !strings.Contains(view, want) {
			t.Fatalf("replay missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "subagent noise") || strings.Contains(view, "command-name") {
		t.Fatalf("replay shows noise:\n%s", view)
	}

	msg := m.gitDiff()()
	if d, ok := msg.(diffMsg); !ok || !strings.Contains(string(d), "a.txt") {
		t.Fatalf("diff = %#v", msg)
	}
}
