//go:build !windows

package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunBashCancelReturnsPromptly(t *testing.T) {
	box := &Toolbox{Cwd: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := runBash(ctx, box, map[string]any{"command": "sleep 30"})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interrupted") {
		t.Fatalf("expected interrupted error, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("runBash took too long to cancel: %v", elapsed)
	}
}

func TestRunBashCancelWithOpenPipeChildReturnsPromptly(t *testing.T) {
	box := &Toolbox{Cwd: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	// Spawns a child that inherits stdout/stderr; WaitDelay guarantees runBash
	// does not hang waiting for pipes to close after SIGKILL.
	_, err := runBash(ctx, box, map[string]any{"command": "(sleep 30) & wait"})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interrupted") {
		t.Fatalf("expected interrupted error, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("runBash with child pipe took too long to cancel: %v", elapsed)
	}
}

func TestRunBashStreamsLiveProgress(t *testing.T) {
	box := &Toolbox{Cwd: t.TempDir()}
	var chunks []string
	ctx := withToolProgress(context.Background(), func(s string) {
		chunks = append(chunks, s)
	})
	out, err := runBash(ctx, box, map[string]any{"command": "echo step1; sleep 0.25; echo step2"})
	if err != nil {
		t.Fatalf("runBash failed: %v", err)
	}
	if !strings.Contains(out, "step1") || !strings.Contains(out, "step2") {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(chunks) == 0 {
		t.Fatal("expected live progress callback to fire while Bash was running")
	}
}

func TestLeanToolsOmitsNonCoreSchemas(t *testing.T) {
	e := &Engine{opts: Options{Cwd: t.TempDir(), LeanTools: true}}
	lean := e.tools(true)
	names := map[string]bool{}
	for _, tl := range lean {
		names[tl.def.Name] = true
	}
	for _, dropped := range []string{"NotebookEdit", "TodoWrite", "WebFetch"} {
		if names[dropped] {
			t.Fatalf("LeanTools should omit %s", dropped)
		}
	}
	for _, core := range []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob", "Task"} {
		if !names[core] {
			t.Fatalf("LeanTools must keep core tool %s", core)
		}
	}
	e.SetLeanTools(false)
	full := e.tools(true)
	if len(full) <= len(lean) {
		t.Fatalf("expected full tools (%d) > lean tools (%d)", len(full), len(lean))
	}
}

func TestPruneTighterWhenSaverActive(t *testing.T) {
	makeEngine := func(lean bool) *Engine {
		e := &Engine{opts: Options{LeanTools: lean}, seen: map[string]readSig{}}
		// 10 tool results: 4 old ones x 7000 chars = 28,000 chars (above 25k saver batch, below 40k default batch).
		for i := 0; i < 10; i++ {
			e.history = append(e.history, Message{Role: "tool", ToolCallID: "c", Result: strings.Repeat("x", 7000)})
		}
		return e
	}
	if dropped := makeEngine(false).pruneResults(false); dropped != 0 {
		t.Fatalf("default mode should wait for 40k batch, dropped %d", dropped)
	}
	if dropped := makeEngine(true).pruneResults(false); dropped < pruneBatchSaver {
		t.Fatalf("saver mode should prune at 25k batch, dropped %d", dropped)
	}
}
