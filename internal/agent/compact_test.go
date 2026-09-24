package agent

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

func TestAutoCompactsNearTheContextLimit(t *testing.T) {
	big := strings.Replace(oaiToolCall("c1", "TodoWrite", `{"todos":[]}`), `"prompt_tokens":100`, `"prompt_tokens":900`, 1)
	s := &script{replies: []string{big, oaiText("the summary"), oaiText("finished")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[{"id":"m","context_length":1000}]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("do the long task")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	if len(s.requests) != 3 || !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "Summarise this conversation") {
		t.Fatalf("no compaction request: %d requests", len(s.requests))
	}
	msgs := fmt.Sprint(s.requests[2]["messages"])
	if !strings.Contains(msgs, "Summary of our conversation so far:\n\nthe summary") || !strings.Contains(msgs, "Continue the task") || strings.Contains(msgs, "do the long task") {
		t.Fatalf("history after compaction: %s", msgs)
	}
	if r.count(func(e claude.Event) bool { c, ok := e.(claude.Compacted); return ok && c.Auto && c.PreTokens == 900 }) != 1 {
		t.Fatal("no Compacted event")
	}
}

func TestCompactsAndRetriesWhenThePromptIsTooLong(t *testing.T) {
	s := &script{replies: []string{"HTTP400 prompt is too long: 250000 tokens > 200000 maximum", oaiText("the summary"), oaiText("answer")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("new question")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	// The pending question is kept out of the summary and asked again after it.
	msgs := s.requests[2]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if len(s.requests) != 3 || last["content"] != "new question" {
		t.Fatalf("retry messages: %v", msgs)
	}
}

func TestDroppingOldToolOutputSparesASummary(t *testing.T) {
	big := strings.Replace(oaiToolCall("c1", "TodoWrite", `{"todos":[]}`), `"prompt_tokens":100`, `"prompt_tokens":900`, 1)
	s := &script{replies: []string{big, oaiText("finished")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[{"id":"m","context_length":1000}]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	<-r.e.ready
	r.e.mu.Lock()
	r.e.history = toolMsgs(8+9, 3500) // too little old output for a routine batch, but dropping it frees far more than the limit needs
	r.e.mu.Unlock()
	r.e.Send("go on")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	if len(s.requests) != 2 || strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "Summarise this conversation") {
		t.Fatalf("expected no summary request, got %d requests", len(s.requests))
	}
	if strings.Count(fmt.Sprint(s.requests[1]["messages"]), "left out to save tokens") != 10 { // the nine, and the one the new result pushed out of the newest eight
		t.Fatal("the old output should have been dropped from the second request")
	}
}
