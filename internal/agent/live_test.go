package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

// LIVE_MODEL=google/gemini-3.5-flash go test ./internal/agent -run Live -v
// Uses the credentials saved by /login.
func TestLiveToolRoundTrip(t *testing.T) {
	model := os.Getenv("LIVE_MODEL")
	if model == "" {
		t.Skip("set LIVE_MODEL")
	}
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("package x\n"), 0o644)
	}
	e, _ := Start(Options{Cwd: dir, Model: model, Mode: "auto", Store: NewStore(filepath.Join(home, ".config", "teveus"))})
	defer e.Close()
	r := &run{t: t, e: e}
	e.Send("Use the Glob tool to find *.go files here, then run `date` with Bash, then reply with the count of .go files only.")
	res := r.until(true)
	tools := r.count(func(ev claude.Event) bool { _, ok := ev.(claude.ToolResults); return ok })
	var text string
	for _, ev := range r.events {
		if am, ok := ev.(claude.AssistantMessage); ok {
			for _, b := range am.Blocks {
				if b.Type == "text" {
					text = b.Text
				}
			}
		}
	}
	t.Logf("result error=%v %q · steps=%d · tool results=%d · final text=%q", res.IsError, res.Text, res.NumTurns, tools, text)
	if res.IsError || tools < 2 {
		t.Fail()
	}
}
