package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The phone page's copy of the transcript is rebuilt on every frame, so a
// block that hasn't changed must not be redacted again: the pattern scan over
// a long conversation measured 52 ms a frame, which is what "the app is laggy
// with the phone page on" was.
func TestPhoneLineIsCachedPerBlock(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	b := &block{kind: kindAssistant, text: "the answer"}
	first, ok := phoneBlock(b, m)
	if !ok || first.Text != "the answer" {
		t.Fatalf("line = %+v, %v", first, ok)
	}
	if !b.phoneOK {
		t.Fatal("the line was not kept")
	}

	// A snapshot on an unchanged block returns the kept line, not a new one.
	again, _ := phoneBlock(b, m)
	if again.Text != first.Text || b.phoneKey != phoneKey(b, m) {
		t.Fatalf("unchanged block rebuilt: %+v", again)
	}

	// Text that changes is rebuilt, so a streamed reply stays current.
	b.text = "the answer, longer"
	got, _ := phoneBlock(b, m)
	if got.Text != "the answer, longer" {
		t.Fatalf("changed block kept the old line: %+v", got)
	}

	// A tool's summary depends on the renderer, so those changes count too.
	tb := &block{kind: kindTool, name: "Bash", input: map[string]any{"command": "ls"},
		result: "a\n", state: toolDone}
	before, _ := phoneBlock(tb, m)
	tb.state = toolFailed
	after, _ := phoneBlock(tb, m)
	if before.State != "done" || after.State != "failed" {
		t.Fatalf("state did not reach the page: %+v / %+v", before, after)
	}
	m.r.expand = true
	expanded, _ := phoneBlock(tb, m)
	if expanded.Text == "" {
		t.Fatal("expanded summary is empty")
	}
}

// Whatever the cache does, a secret still never reaches the page.
func TestPhoneLineStaysRedacted(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	b := &block{kind: kindAssistant, text: "the key is sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}
	if line, _ := phoneBlock(b, m); contains(line.Text, "abcdefghijklmnop") {
		t.Fatalf("key reached the phone: %q", line.Text)
	}
	// A second snapshot of the same block must not un-redact it either.
	b.text += " (still here)"
	if line, _ := phoneBlock(b, m); contains(line.Text, "abcdefghijklmnop") {
		t.Fatalf("key reached the phone after a change: %q", line.Text)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
