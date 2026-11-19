package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

// A real provider, a real Task call: does the main model hand work to a
// subagent and use its report?
//
//	LIVE_MODEL=deepseek/deepseek-chat go test ./internal/agent -run LiveSubagent -v
//	LIVE_SUBAGENT=deepseek/deepseek-chat   # optional, defaults to LIVE_MODEL
//
// Uses the credentials saved by /login, like the other live tests.
func TestLiveSubagent(t *testing.T) {
	model := os.Getenv("LIVE_MODEL")
	if model == "" {
		t.Skip("set LIVE_MODEL")
	}
	sub := os.Getenv("LIVE_SUBAGENT")
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	// Three files with a distinctive marker in one of them, so the answer can
	// only come from reading them.
	writeFile(t, filepath.Join(dir, "alpha.go"), "package x\n// nothing\n")
	writeFile(t, filepath.Join(dir, "beta.go"), "package x\nconst Marker = \"PINEAPPLE-42\"\n")
	writeFile(t, filepath.Join(dir, "gamma.go"), "package x\n// nothing\n")

	e, err := Start(Options{Cwd: dir, Model: model, Mode: "auto", SubagentModel: sub,
		Store: NewStore(filepath.Join(home, ".config", "teveus"))})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Close()
	r := &run{t: t, e: e}
	e.Send("Use the Task tool to start a subagent that searches these .go files for a constant named Marker, " +
		"and report its exact value. Do not read the files yourself: use Task.")
	res := r.until(true)

	tasks := r.count(func(ev claude.Event) bool {
		am, ok := ev.(claude.AssistantMessage)
		if !ok {
			return false
		}
		for _, b := range am.Blocks {
			if b.Type == "tool_use" && b.Name == "Task" && am.Parent == "" {
				return true
			}
		}
		return false
	})
	nested := r.count(func(ev claude.Event) bool {
		am, ok := ev.(claude.AssistantMessage)
		return ok && am.Parent != ""
	})
	final := ""
	for _, ev := range r.events {
		if am, ok := ev.(claude.AssistantMessage); ok && am.Parent == "" {
			for _, b := range am.Blocks {
				if b.Type == "text" {
					final = b.Text
				}
			}
		}
	}
	t.Logf("model=%s subagent=%q · Task calls=%d · subagent steps=%d · error=%v",
		model, sub, tasks, nested, res.IsError)
	t.Logf("final reply: %s", strings.TrimSpace(final))

	if res.IsError {
		t.Fatalf("turn failed: %s", res.Text)
	}
	if tasks == 0 {
		t.Skip("the model answered without a subagent; not a failure, just not this test")
	}
	if nested == 0 {
		t.Error("a Task call ran but the subagent produced no events")
	}
	if !strings.Contains(final, "PINEAPPLE-42") {
		t.Errorf("the subagent's finding did not reach the final answer: %q", final)
	}
}
