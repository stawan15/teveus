package agent

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func toolMsgs(n, size int) []Message {
	var h []Message
	for i := range n {
		id := fmt.Sprintf("c%d", i)
		h = append(h,
			Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "Read"}}},
			Message{Role: "tool", ToolCallID: id, Result: strings.Repeat("x", size)})
	}
	return h
}

func pruned(h []Message) int {
	n := 0
	for _, m := range h {
		if m.Pruned {
			n++
		}
	}
	return n
}

func TestPruneWaitsForABatch(t *testing.T) {
	e := &Engine{seen: map[string]readSig{}}
	e.history = toolMsgs(12, 2000) // 4 old results = 8k characters: not worth breaking the cache yet
	if got := e.pruneResults(false); got != 0 || pruned(e.history) != 0 {
		t.Fatalf("pruned %d characters too early", got)
	}
	e.history = toolMsgs(8+25, 2000) // 25 old results = 50k characters
	if got := e.pruneResults(false); got != 50000 || pruned(e.history) != 25 {
		t.Fatalf("pruned %d characters, %d results", got, pruned(e.history))
	}
	// The newest results stay whole, and the stored originals are kept.
	sent := withoutPruned(e.history)
	last := sent[len(sent)-1]
	if last.Pruned || len(last.Result) != 2000 {
		t.Fatalf("a recent result was dropped: %+v", last.Pruned)
	}
	if old := sent[1]; !strings.Contains(old.Result, "2000 characters") || len(old.Result) > 120 {
		t.Fatalf("old result sent as %q", old.Result)
	}
	if len(e.history[1].Result) != 2000 {
		t.Fatal("the stored history lost the original")
	}
	// Nothing new piled up, so the sent prefix stays the same.
	if got := e.pruneResults(false); got != 0 {
		t.Fatalf("pruned again: %d", got)
	}
}

func TestPruneSkipsSmallResultsAndForceTakesAll(t *testing.T) {
	e := &Engine{seen: map[string]readSig{}}
	e.history = toolMsgs(20, 100)
	if e.pruneResults(true) != 0 {
		t.Fatal("small results should never be dropped")
	}
	e.history = toolMsgs(10, 3000)
	if got := e.pruneResults(true); got != 2*3000 || pruned(e.history) != 2 {
		t.Fatalf("force pruned %d characters", got)
	}
}

func TestUnchangedReadIsNotSentTwice(t *testing.T) {
	big := "first\n" + strings.Repeat("line\n", 100)
	s := &script{replies: []string{
		oaiToolCall("c1", "Read", `{"file_path":"a.txt"}`),
		oaiToolCall("c2", "Read", `{"file_path":"a.txt"}`),
		oaiToolCall("c3", "Read", `{"file_path":"a.txt","limit":5}`),
		oaiToolCall("c4", "Edit", `{"file_path":"a.txt","old_string":"first","new_string":"FIRST"}`),
		oaiToolCall("c5", "Read", `{"file_path":"a.txt","limit":5}`),
		oaiText("done"),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, dir := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "auto")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(big), 0o644)
	r.e.Send("go")
	r.until(true)

	result := func(callID string) string {
		for _, m := range r.e.history {
			if m.Role == "tool" && m.ToolCallID == callID {
				return m.Result
			}
		}
		return ""
	}
	if !strings.Contains(result("c1"), "line") || len(result("c1")) < 500 {
		t.Fatalf("first read = %q", result("c1"))
	}
	if result("c2") != unchangedNote {
		t.Fatalf("an unchanged file was sent again: %q", result("c2"))
	}
	if strings.Contains(result("c3"), "unchanged") || !strings.Contains(result("c3"), "1\tfirst") {
		t.Fatalf("a different range must be sent: %q", result("c3"))
	}
	if !strings.HasPrefix(result("c5"), "     1\tFIRST") {
		t.Fatalf("a file changed by Edit must be sent again: %q", result("c5"))
	}
}

func TestPrunedNotesReachTheModel(t *testing.T) {
	s := &script{replies: []string{oaiText("ok")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "auto")
	<-r.e.ready
	r.e.mu.Lock()
	r.e.history = toolMsgs(8+25, 2000)
	r.e.mu.Unlock()
	r.e.Send("continue")
	r.until(true)
	body := fmt.Sprint(s.requests[0]["messages"])
	if strings.Count(body, "left out to save tokens") != 25 {
		t.Fatalf("the request carried %d notes, want 25", strings.Count(body, "left out to save tokens"))
	}
	if strings.Count(body, strings.Repeat("x", 2000)) != 8 {
		t.Fatalf("the newest 8 results should be sent whole")
	}
}

func TestSubagentModelIsOptIn(t *testing.T) {
	for _, tc := range []struct{ sub, want string }{{"", "main-model"}, {"custom/cheap-model", "cheap-model"}, {"custom", "main-model"}} {
		s := &script{replies: []string{
			oaiToolCall("c1", "Task", `{"description":"look","prompt":"find things"}`),
			oaiText("report"),
			oaiText("done"),
		}}
		srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
		r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/main-model", "auto")
		r.e.SetSubagentModel(tc.sub)
		r.e.Send("go")
		r.until(true)
		srv.Close()
		if got := s.requests[0]["model"]; got != "main-model" {
			t.Errorf("sub=%q: the conversation ran on %v", tc.sub, got)
		}
		if got := s.requests[1]["model"]; got != tc.want {
			t.Errorf("sub=%q: the subagent ran on %v, want %s", tc.sub, got, tc.want)
		}
	}
}
