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

func TestPreviewRewindListsWhatWillChange(t *testing.T) {
	dir := t.TempDir()
	edited := filepath.Join(dir, "edited.txt")
	created := filepath.Join(dir, "created-later.txt")
	restored := filepath.Join(dir, "restored.txt")
	same := filepath.Join(dir, "same.txt")
	binary := filepath.Join(dir, "image.bin")
	big := filepath.Join(dir, "big.txt")
	os.WriteFile(edited, []byte("second"), 0o644)
	os.WriteFile(created, []byte("made in second"), 0o644)
	os.WriteFile(same, []byte("same"), 0o644)
	os.WriteFile(binary, []byte("new\x00data"), 0o644)
	os.WriteFile(big, []byte(strings.Repeat("new\n", rewindPreviewLimit/4)), 0o644)

	e := rewindEngine(t, dir,
		[]Message{
			{Role: "user", Text: "first prompt"},
			{Role: "assistant", Text: "first answer"},
			{Role: "user", Text: "second prompt"},
			{Role: "assistant", Text: "second answer"},
		},
		&checkpoint{historyLen: 0, files: map[string][]byte{edited: []byte("before first")}, order: []string{edited}},
		&checkpoint{historyLen: 2, files: map[string][]byte{
			edited:   []byte("after first"),
			created:  nil,
			restored: []byte("back again\n"),
			same:     []byte("same"),
			binary:   []byte("old\x00data"),
			big:      []byte(strings.Repeat("old\n", rewindPreviewLimit/4)),
		}, order: []string{edited, created, restored, same, binary, big}},
	)

	beforeHistory := len(e.history)
	beforeChecks := len(e.checkpoints)
	prev, err := e.PreviewRewind(1)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Prompt != "second prompt" {
		t.Fatalf("prompt = %q", prev.Prompt)
	}
	got := map[string]RewindFile{}
	for _, f := range prev.Files {
		got[filepath.Base(f.Path)] = f
	}
	if len(prev.Files) != 5 {
		t.Fatalf("files = %+v", prev.Files)
	}
	if f := got["edited.txt"]; f.Status != RewindModified || f.Added != 1 || f.Removed != 1 ||
		string(f.Before) != "second" || string(f.After) != "after first" {
		t.Fatalf("edited = %+v", f)
	}
	if f := got["created-later.txt"]; f.Status != RewindDeleted || f.Removed != 1 || f.After != nil {
		t.Fatalf("created = %+v", f)
	}
	if f := got["restored.txt"]; f.Status != RewindRestored || f.Added != 1 || string(f.After) != "back again\n" {
		t.Fatalf("restored = %+v", f)
	}
	if f := got["image.bin"]; !f.Binary || f.Before != nil || f.After != nil {
		t.Fatalf("binary = %+v", f)
	}
	if f := got["big.txt"]; !f.TooLarge || f.Before != nil || f.After != nil {
		t.Fatalf("big = %+v", f)
	}
	if _, ok := got["same.txt"]; ok {
		t.Fatal("unchanged file was included")
	}
	if prev.Added != 2 || prev.Removed != 2 {
		t.Fatalf("totals = +%d -%d", prev.Added, prev.Removed)
	}
	if _, err := os.Stat(restored); !os.IsNotExist(err) {
		t.Fatal("preview restored a file")
	}
	if b, _ := os.ReadFile(edited); string(b) != "second" {
		t.Fatalf("preview changed edited.txt: %q", b)
	}
	if len(e.history) != beforeHistory || len(e.checkpoints) != beforeChecks {
		t.Fatal("preview changed the conversation")
	}
}

func TestPreviewRewindMatchesRewindTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("current"), 0o644)
	e := rewindEngine(t, dir, []Message{{Role: "user", Text: "one"}, {Role: "user", Text: "two"}},
		&checkpoint{historyLen: 0, files: map[string][]byte{path: []byte("zero")}, order: []string{path}},
		&checkpoint{historyLen: 1, files: map[string][]byte{path: []byte("one")}, order: []string{path}},
	)
	prev, err := e.PreviewRewind(1)
	if err != nil || len(prev.Files) != 1 || string(prev.Files[0].After) != "one" {
		t.Fatalf("preview = %+v, %v", prev, err)
	}
	if _, err := e.RewindTo(1); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(prev.Files[0].After) {
		t.Fatalf("restored %q, preview said %q", b, prev.Files[0].After)
	}
}

func TestPreviewRewindRejectsBadState(t *testing.T) {
	e := rewindEngine(t, t.TempDir(), nil)
	if _, err := e.PreviewRewind(0); err == nil {
		t.Fatal("expected an error")
	}
	e = rewindEngine(t, t.TempDir(), []Message{{Role: "user", Text: "one"}}, &checkpoint{historyLen: 0, files: map[string][]byte{}})
	e.cancel = func() {}
	if _, err := e.PreviewRewind(0); err == nil || !strings.Contains(err.Error(), "wait") {
		t.Fatalf("err = %v", err)
	}
}

func TestRewindSkipsFilesAlreadyAtTheirCheckpoint(t *testing.T) {
	dir := t.TempDir()
	same := filepath.Join(dir, "same.txt")
	edited := filepath.Join(dir, "edited.txt")
	os.WriteFile(same, []byte("same\n"), 0o644)
	os.WriteFile(edited, []byte("now\n"), 0o644)
	e := rewindEngine(t, dir, []Message{{Role: "user", Text: "one"}},
		&checkpoint{historyLen: 0, files: map[string][]byte{same: []byte("same\n"), edited: []byte("before\n")},
			order: []string{same, edited}},
	)
	prev, err := e.PreviewRewind(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Files) != 1 || filepath.Base(prev.Files[0].Path) != "edited.txt" {
		t.Fatalf("preview = %+v", prev.Files)
	}
	res, err := e.RewindTo(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 || filepath.Base(res.Restored[0]) != "edited.txt" {
		t.Fatalf("restored = %+v", res.Restored)
	}
	if b, _ := os.ReadFile(same); string(b) != "same\n" {
		t.Fatalf("same.txt = %q", b)
	}
}
