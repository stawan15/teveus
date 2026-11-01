package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ledger is read by other programs (the claude-monitor app), so the field
// names and shape are a contract: this test pins both.
func TestUsageRecordShape(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{
		opts:    Options{Cwd: "/Users/me/app", ConfigDir: dir},
		session: "api-abc",
		model:   "anthropic/claude-sonnet-5",
	}
	e.recordUsage("claude-sonnet-5", Usage{Input: 12, Output: 340, CacheRead: 29570, CacheWrite: 9140, Cost: 0.0342}, false)

	b, err := os.ReadFile(UsagePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("want one line, got %d", n)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"time", "id", "session", "provider", "model", "project",
		"cwd", "input", "output", "cacheRead", "cacheWrite", "cost", "subagent"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if m["provider"] != "anthropic" || m["project"] != "app" || m["model"] != "claude-sonnet-5" {
		t.Errorf("unexpected values: %v", m)
	}
	if _, err := time.Parse(time.RFC3339Nano, m["time"].(string)); err != nil {
		t.Errorf("time is not RFC 3339: %v", err)
	}
}

func TestUsageRecordSkipsEmptyAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{opts: Options{ConfigDir: dir}, session: "s", model: "anthropic/m"}
	e.recordUsage("m", Usage{}, false)
	if _, err := os.Stat(UsagePath(dir)); !os.IsNotExist(err) {
		t.Fatal("an all-zero response should not be written")
	}

	e.opts.ConfigDir = ""
	e.recordUsage("m", Usage{Input: 1}, false) // no config dir: no ledger, no panic
}

func TestUsageRecordAppends(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{opts: Options{Cwd: "/x/app", ConfigDir: dir}, session: "s", model: "openai/gpt-5"}
	for range 3 {
		e.recordUsage("gpt-5", Usage{Input: 1, Output: 1}, true)
	}
	b, err := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 3 {
		t.Fatalf("want 3 lines, got %d", n)
	}
}
