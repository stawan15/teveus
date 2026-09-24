package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBackgroundJob(t *testing.T) {
	box := NewToolbox(t.TempDir())
	t.Cleanup(box.closeJobs)

	out, err := runBash(context.TODO(), box, map[string]any{"command": "echo one; sleep 0.3; echo two; sleep 30", "run_in_background": true})
	if err != nil || !strings.Contains(out, "bash_1") {
		t.Fatalf("start = %q, %v", out, err)
	}
	poll := func() string {
		res, err := runBashOutput(context.TODO(), box, map[string]any{"bash_id": "bash_1"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if got := poll(); !strings.Contains(got, "running") {
		t.Fatalf("first poll = %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	var seen string
	for !strings.Contains(seen, "two") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		seen += poll()
	}
	if !strings.Contains(seen, "two") {
		t.Fatalf("never saw the later output: %q", seen)
	}
	if strings.Count(seen, "\none\n") > 1 {
		t.Fatalf("output was returned twice: %q", seen)
	}

	if _, err := runKillShell(context.TODO(), box, map[string]any{"shell_id": "bash_1"}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(poll(), "stopped") {
		if time.Now().After(deadline) {
			t.Fatal("the killed command never reported stopped")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := runBashOutput(context.TODO(), box, map[string]any{"bash_id": "nope"}); err == nil {
		t.Fatal("an unknown id should be an error")
	}
}

func TestJobKeepsOnlyTheTail(t *testing.T) {
	j := &job{}
	chunk := strings.Repeat("x", 1000)
	for range 1500 {
		j.Write([]byte(chunk))
	}
	if len(j.buf) > jobKeep || j.dropped+len(j.buf) != 1500*1000 {
		t.Fatalf("buf=%d dropped=%d", len(j.buf), j.dropped)
	}
}

func TestSubagentsCannotTouchBackgroundJobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	e := &Engine{opts: Options{Cwd: t.TempDir()}, mcp: nil}
	var names []string
	for _, tl := range e.subagentTools() {
		names = append(names, tl.def.Name)
	}
	for _, banned := range []string{"BashOutput", "KillShell", "Skill", "TodoWrite", "Bash", "Write", "Edit", "NotebookEdit"} {
		for _, n := range names {
			if n == banned {
				t.Errorf("a research subagent got %s", n)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("a subagent needs its read-only tools")
	}
}
