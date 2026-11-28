package antigravity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

type nopWriteCloser struct{ bytes.Buffer }

func (n *nopWriteCloser) Close() error { return nil }

func TestInterruptSendsNonBlockingACPNotification(t *testing.T) {
	var buf nopWriteCloser
	c := &Client{
		stdin:   &buf,
		session: "ses-test-1",
	}

	done := make(chan error, 1)
	go func() { done <- c.Interrupt() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Interrupt blocked waiting for a JSON-RPC reply; session/cancel must be a notification")
	}

	var msg map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatalf("invalid JSON written: %v (%q)", err, buf.String())
	}
	if msg["method"] != "session/cancel" {
		t.Fatalf("method = %v, want session/cancel", msg["method"])
	}
	if _, hasID := msg["id"]; hasID {
		t.Fatalf("session/cancel must be a notification without id, got %v", msg)
	}
	params, _ := msg["params"].(map[string]any)
	if params["sessionId"] != "ses-test-1" {
		t.Fatalf("sessionId = %v, want ses-test-1", params["sessionId"])
	}
}

func TestStartReturnsImmediatelyBeforeHandshake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is unix-only")
	}
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-agy")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c, err := Start(Options{Binary: fakeBin, Cwd: dir})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer c.Close()
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Start blocked for %v; handshake must run asynchronously", elapsed)
	}
}

func TestSettersAreNonBlocking(t *testing.T) {
	var buf nopWriteCloser
	ready := make(chan struct{})
	close(ready)
	c := &Client{
		stdin:         &buf,
		events:        make(chan claude.Event, 8),
		ready:         ready,
		session:       "ses-async",
		thoughtLevels: []string{"low", "medium", "high"},
	}
	start := time.Now()
	_ = c.SetPermissionMode("auto")
	_ = c.SetModel("gemini-4-neon")
	_ = c.SetEffort("high")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("setters blocked UI thread for %v", elapsed)
	}
	c.mu.Lock()
	gotMode, gotModel, gotEffort := c.mode, c.model, c.effort
	c.mu.Unlock()
	if gotMode != "auto" || gotModel != "gemini-4-neon" || gotEffort != "high" {
		t.Fatalf("state not updated immediately: mode=%q model=%q effort=%q", gotMode, gotModel, gotEffort)
	}
}
