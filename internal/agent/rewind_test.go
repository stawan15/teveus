package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rewindEngine(t *testing.T, dir string, history []Message, cps ...*checkpoint) *Engine {
	t.Helper()
	return &Engine{
		opts:        Options{Cwd: dir, SessionDir: t.TempDir()},
		session:     "api-rewind-test",
		history:     history,
		checkpoints: cps,
		seen:        map[string]readSig{},
	}
}

func TestRewindToRestoresFilesAndDropsLaterTurns(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	fresh := filepath.Join(dir, "fresh.txt")
	os.WriteFile(keep, []byte("after second"), 0o644)
	os.WriteFile(fresh, []byte("made in second"), 0o644)

	e := rewindEngine(t, dir,
		[]Message{
			{Role: "user", Text: "first prompt"},
			{Role: "assistant", Text: "first answer"},
			{Role: "user", Text: "second prompt"},
			{Role: "assistant", Text: "second answer"},
		},
		&checkpoint{historyLen: 0, files: map[string][]byte{keep: []byte("before first")}, order: []string{keep}},
		&checkpoint{historyLen: 2, files: map[string][]byte{keep: []byte("after first"), fresh: nil}, order: []string{keep, fresh}},
	)

	res, err := e.RewindTo(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != "second prompt" {
		t.Fatalf("prompt = %q", res.Prompt)
	}
	if len(res.History) != 2 || res.History[1].Text != "first answer" {
		t.Fatalf("history = %+v", res.History)
	}
	if len(e.checkpoints) != 1 {
		t.Fatalf("checkpoints = %d, want 1", len(e.checkpoints))
	}
	if b, _ := os.ReadFile(keep); string(b) != "after first" {
		t.Fatalf("keep.txt = %q", b)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatal("fresh.txt should have been removed")
	}

	res, err = e.RewindTo(0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != "first prompt" {
		t.Fatalf("prompt = %q", res.Prompt)
	}
	if len(res.History) != 0 || len(e.checkpoints) != 0 {
		t.Fatalf("history/checkpoints = %d/%d, want 0/0", len(res.History), len(e.checkpoints))
	}
	if b, _ := os.ReadFile(keep); string(b) != "before first" {
		t.Fatalf("keep.txt = %q", b)
	}
}

func TestRewindPointsListsPromptsAndFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	e := rewindEngine(t, dir,
		[]Message{{Role: "user", Text: "one"}, {Role: "user", Text: "two"}},
		&checkpoint{historyLen: 0, files: map[string][]byte{a: nil}, order: []string{a}},
		&checkpoint{historyLen: 1, files: map[string][]byte{a: nil, b: nil}, order: []string{a, b}},
	)
	got := e.RewindPoints()
	if len(got) != 2 {
		t.Fatalf("points = %+v", got)
	}
	if got[0] != (RewindPoint{Index: 0, Prompt: "one", Files: 1}) {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1] != (RewindPoint{Index: 1, Prompt: "two", Files: 2}) {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestRewindRefusesWhileTurnRuns(t *testing.T) {
	dir := t.TempDir()
	e := rewindEngine(t, dir, []Message{{Role: "user", Text: "one"}},
		&checkpoint{historyLen: 0, files: map[string][]byte{}})
	e.cancel = func() {}
	if _, err := e.RewindTo(0); err == nil || !strings.Contains(err.Error(), "wait") {
		t.Fatalf("err = %v", err)
	}
}

func TestRewindRejectsMissingTurn(t *testing.T) {
	dir := t.TempDir()
	e := rewindEngine(t, dir, nil)
	if _, err := e.RewindTo(0); err == nil {
		t.Fatal("expected an error")
	}
	if points := e.RewindPoints(); len(points) != 0 {
		t.Fatalf("points = %+v", points)
	}
}
