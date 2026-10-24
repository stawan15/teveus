package agent

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hookCommand(t *testing.T, name, unix, windows string) string {
	t.Helper()
	content, ext := unix, ".sh"
	if runtime.GOOS == "windows" {
		content, ext = windows, ".bat"
	}
	path := filepath.Join(t.TempDir(), name+ext)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

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
		oaiToolCall("c1", "Bash", `{"command":"echo x > ran"}`),
		oaiText("ok"),
	}}
	pre := hookCommand(t, "pre",
		"#!/bin/sh\necho no touching >&2\nexit 2\n",
		"@echo off\r\necho no touching 1>&2\r\nexit /b 2\r\n")
	preJSON, _ := json.Marshal(pre)
	r, dir := startWithHooks(t, s, fmt.Sprintf(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":%s}]}]}}`, preJSON))
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
	post := hookCommand(t, "post",
		"#!/bin/sh\nif [ -f a.txt ]; then\n  echo \"lint: a.txt is bad\" >&2\nfi\nexit 1\n",
		"@echo off\r\nif exist a.txt echo lint: a.txt is bad 1>&2\r\nexit /b 1\r\n")
	postJSON, _ := json.Marshal(post)
	r, dir := startWithHooks(t, s, fmt.Sprintf(`{"hooks":{"PostToolUse":[{"matcher":"Edit|Write","hooks":[{"command":%s}]}]}}`, postJSON))
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
