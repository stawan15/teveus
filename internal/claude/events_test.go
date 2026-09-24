package claude

import (
	"bytes"
	"strings"
	"testing"
)

type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

func decode(t *testing.T, line string) ([]Event, string) {
	t.Helper()
	var sent bytes.Buffer
	c := &Client{stdin: nopCloser{&sent}}
	return c.decode([]byte(line)), sent.String()
}

func one[T Event](t *testing.T, line string) T {
	t.Helper()
	evs, _ := decode(t, line)
	if len(evs) != 1 {
		t.Fatalf("got %d events for %s", len(evs), line)
	}
	v, ok := evs[0].(T)
	if !ok {
		t.Fatalf("got %T for %s", evs[0], line)
	}
	return v
}

func TestDecodeInit(t *testing.T) {
	e := one[Init](t, `{"type":"system","subtype":"init","session_id":"s1","model":"m","cwd":"/p","permissionMode":"plan","tools":["Read","Bash"]}`)
	if e.SessionID != "s1" || e.Model != "m" || e.Cwd != "/p" || e.PermissionMode != "plan" || len(e.Tools) != 2 {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeCompactBoundary(t *testing.T) {
	e := one[Compacted](t, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":900}}`)
	if !e.Auto || e.PreTokens != 900 {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeStreamEvents(t *testing.T) {
	b := one[BlockStart](t, `{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Bash"}}}`)
	if b.Kind != "tool_use" || b.ToolName != "Bash" {
		t.Fatalf("%+v", b)
	}
	d := one[TextDelta](t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}`)
	if d.Text != "hi" {
		t.Fatalf("%+v", d)
	}
	if evs, _ := decode(t, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta"}}}`); evs != nil {
		t.Fatalf("non-text delta should be dropped: %v", evs)
	}
}

func TestDecodeSubagentStreamingDropped(t *testing.T) {
	line := `{"type":"stream_event","parent_tool_use_id":"toolu_1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}}`
	if evs, _ := decode(t, line); evs != nil {
		t.Fatalf("subagent stream should be dropped: %v", evs)
	}
}

func TestDecodeAssistantWithParent(t *testing.T) {
	e := one[AssistantMessage](t, `{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"a"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":5}}}`)
	if e.Parent != "toolu_1" || len(e.Blocks) != 2 || e.Blocks[1].Name != "Read" {
		t.Fatalf("%+v", e)
	}
	if e.Usage == nil || e.Usage.Context() != 15 {
		t.Fatalf("usage %+v", e.Usage)
	}
}

func TestDecodeUserToolResults(t *testing.T) {
	e := one[ToolResults](t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"out","is_error":true},{"type":"text","text":"ignored"}]}}`)
	if len(e.Results) != 1 || e.Results[0].ResultText() != "out" || !e.Results[0].IsError {
		t.Fatalf("%+v", e)
	}
	if evs, _ := decode(t, `{"type":"user","message":{"content":"plain echo"}}`); evs != nil {
		t.Fatalf("plain user echo should be dropped: %v", evs)
	}
}

func TestResultTextBlocks(t *testing.T) {
	e := one[ToolResults](t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}]}}`)
	if got := e.Results[0].ResultText(); got != "a\n[image]\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestDecodePermissionRequest(t *testing.T) {
	e := one[*PermissionRequest](t, `{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"t1","description":"run ls","input":{"command":"ls"}}}`)
	if e.RequestID != "r1" || e.ToolName != "Bash" || e.ToolUseID != "t1" || e.Description != "run ls" {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeUnsupportedControlRequestIsRefused(t *testing.T) {
	evs, sent := decode(t, `{"type":"control_request","request_id":"r2","request":{"subtype":"hook_callback"}}`)
	if evs != nil {
		t.Fatalf("got %v", evs)
	}
	if !strings.Contains(sent, `"error"`) || !strings.Contains(sent, "r2") || !strings.Contains(sent, "hook_callback") {
		t.Fatalf("no error reply sent: %q", sent)
	}
}

func TestDecodeControlResponses(t *testing.T) {
	r := one[Ready](t, `{"type":"control_response","response":{"subtype":"success","request_id":"init-1","response":{"commands":[{"name":"compact"}],"models":[{"value":"m","displayName":"M"}],"current_permission_mode":"default"}}}`)
	if len(r.Commands) != 1 || r.Models[0].DisplayName != "M" || r.PermissionMode != "default" {
		t.Fatalf("%+v", r)
	}
	c := one[ControlResult](t, `{"type":"control_response","response":{"subtype":"error","request_id":"mode-3","error":"nope"}}`)
	if c.Tag != "mode" || c.Error != "nope" {
		t.Fatalf("%+v", c)
	}
}

func TestDecodeRateLimit(t *testing.T) {
	e := one[RateLimit](t, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.5,"resetsAt":100}}}}`)
	if e.Status != "allowed" || e.FiveHour == nil || e.FiveHour.Utilization != 0.5 || e.SevenDay != nil {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeResult(t *testing.T) {
	e := one[Result](t, `{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.25,"duration_ms":1200,"num_turns":3,"usage":{"input_tokens":7,"output_tokens":9}}`)
	if e.Text != "done" || e.CostUSD != 0.25 || e.DurationMS != 1200 || e.NumTurns != 3 || e.Usage.OutputTokens != 9 {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeGarbageIsSurfaced(t *testing.T) {
	e := one[Unknown](t, `not json`)
	if e.Line != "not json" {
		t.Fatalf("%+v", e)
	}
}
