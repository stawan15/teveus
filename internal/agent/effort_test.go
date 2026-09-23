package agent

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestAnthropicEffortAndThinkingBlocks(t *testing.T) {
	start := `data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`
	s := &script{replies: []string{
		"HTTP400 output_config.effort: this model does not support effort",
		anthropicSSE(start,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"look for go files"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"Glob"}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"*.go\"}"}}`,
			`data: {"type":"content_block_stop","index":1}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`),
		anthropicSSE(start,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"None."}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	store := NewStore(t.TempDir())
	store.Save("anthropic", Credential{Key: "k", BaseURL: srv.URL})
	e, _ := Start(Options{Cwd: t.TempDir(), Model: "anthropic/claude-x", Mode: "default", Store: store, Effort: "xhigh"})
	t.Cleanup(e.Close)
	r := &run{t: t, e: e}
	e.Send("any go files?")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	// Sent with effort, retried without it when the model refused the field.
	if fmt.Sprint(s.requests[0]["output_config"]) != "map[effort:xhigh]" || s.requests[1]["output_config"] != nil {
		t.Fatalf("effort: %v / %v", s.requests[0]["output_config"], s.requests[1]["output_config"])
	}
	// The thinking block, with its signature, goes back before the tool call.
	msgs := s.requests[2]["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	first := asst[0].(map[string]any)
	if first["type"] != "thinking" || first["signature"] != "sig123" || first["thinking"] != "look for go files" || asst[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant turn: %v", asst)
	}
}

func TestOpenAIEffortMapping(t *testing.T) {
	for _, c := range []struct{ provider, effort, field, want string }{
		{"custom", "max", "reasoning_effort", "high"},
		{"custom", "low", "reasoning_effort", "low"},
		{"openrouter", "xhigh", "reasoning", "map[effort:high]"},
	} {
		s := &script{replies: []string{oaiText("ok")}}
		srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
		store := NewStore(t.TempDir())
		store.Save(c.provider, Credential{Key: "k", BaseURL: srv.URL})
		e, _ := Start(Options{Cwd: t.TempDir(), Model: c.provider + "/m", Mode: "default", Store: store, Effort: c.effort})
		r := &run{t: t, e: e}
		e.Send("hi")
		r.until(true)
		if got := fmt.Sprint(s.requests[0][c.field]); got != c.want {
			t.Errorf("%s %s: %s = %s", c.provider, c.effort, c.field, got)
		}
		e.Close()
		srv.Close()
	}
	// No effort set: nothing is sent.
	if openaiEffort("") != "" {
		t.Fatal("empty effort mapped")
	}
}

func TestReasoningStaysWithItsProvider(t *testing.T) {
	anth := []byte(`[{"type":"thinking","thinking":"x","signature":"s"}]`)
	router := []byte(`[{"type":"reasoning.text","text":"x"}]`)
	if routerReasoning(anth) != nil || routerReasoning(router) == nil {
		t.Fatal("routerReasoning")
	}
	if len(anthropicThinking(router)) != 0 || len(anthropicThinking(anth)) != 1 {
		t.Fatal("anthropicThinking")
	}
}
