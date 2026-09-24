package agent

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startWithHooks starts an engine whose config directory holds hooks.json.
func startWithHooks(t *testing.T, s *script, hooksJSON string) (*run, string) {
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	t.Cleanup(srv.Close)
	store, cfg, dir := NewStore(t.TempDir()), t.TempDir(), t.TempDir()
	store.Save("custom", Credential{BaseURL: srv.URL})
	os.WriteFile(filepath.Join(cfg, "hooks.json"), []byte(hooksJSON), 0o600)
	e, _ := Start(Options{Cwd: dir, Model: "custom/m", Mode: "auto", Store: store, ConfigDir: cfg})
	t.Cleanup(e.Close)
	return &run{t: t, e: e}, dir
}

func TestPreToolUseHookBlocks(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("c1", "Bash", `{"command":"touch ran"}`),
		oaiText("ok"),
	}}
	r, dir := startWithHooks(t, s, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo no touching >&2; exit 2"}]}]}}`)
	r.e.Send("go")
	r.until(true)
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Fatal("the blocked command ran")
	}
	if !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "Blocked by a PreToolUse hook: no touching") {
		t.Fatalf("the model was not told why: %v", s.requests[1]["messages"])
	}
}

func TestPostToolUseHookFeedback(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("c1", "Write", `{"file_path":"a.txt","content":"x"}`),
		oaiText("ok"),
	}}
	// The hook gets the call on stdin and can fail the way a linter would.
	r, dir := startWithHooks(t, s, `{"hooks":{"PostToolUse":[{"matcher":"Edit|Write","hooks":[{"command":"grep -q a.txt && echo lint: a.txt is bad >&2; exit 1"}]}]}}`)
	r.e.Send("go")
	r.until(true)
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("the write should still happen")
	}
	if !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "lint: a.txt is bad") {
		t.Fatalf("the model did not see the hook's output: %v", s.requests[1]["messages"])
	}
}

func TestHookMatcherAndProjectScope(t *testing.T) {
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "hooks.json"), []byte(`{
		"hooks": {"PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"command": "a"}]}]},
		"projects": {"/here": {"hooks": {"PostToolUse": [{"hooks": [{"command": "b"}]}]}}}}`), 0o600)
	if got := newHooks(cfg, "/here").commands("PostToolUse", "Write"); len(got) != 2 {
		t.Errorf("Write in /here = %v", got)
	}
	if got := newHooks(cfg, "/here").commands("PostToolUse", "Bash"); len(got) != 1 || got[0] != "b" {
		t.Errorf("Bash in /here = %v", got)
	}
	if got := newHooks(cfg, "/elsewhere").commands("PostToolUse", "Bash"); len(got) != 0 {
		t.Errorf("Bash elsewhere = %v", got)
	}
	if matcherFits("Edit", "MultiEdit") {
		t.Error("a matcher must match the whole tool name")
	}
}
