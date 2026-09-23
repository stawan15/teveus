package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

// script is a mock provider: each request gets the next scripted SSE reply,
// and the request bodies are kept for assertions.
type script struct {
	mu       sync.Mutex
	replies  []string
	requests []map[string]any
}

func (s *script) handler(t *testing.T, models string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			io.WriteString(w, models)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.requests = append(s.requests, body)
		if len(s.replies) == 0 {
			s.mu.Unlock()
			t.Errorf("unexpected extra request")
			http.Error(w, "no more replies", 500)
			return
		}
		reply := s.replies[0]
		s.replies = s.replies[1:]
		s.mu.Unlock()
		// "HTTP429 message" answers with that status instead of a stream.
		if code, msg, ok := strings.Cut(strings.TrimPrefix(reply, "HTTP"), " "); ok && strings.HasPrefix(reply, "HTTP") {
			status, _ := strconv.Atoi(code)
			http.Error(w, `{"error":{"message":"`+msg+`"}}`, status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range strings.Split(reply, "\n") {
			fmt.Fprintf(w, "%s\n\n", line)
			w.(http.Flusher).Flush()
		}
	}
}

func oaiChunk(delta string) string {
	return `data: {"choices":[{"delta":` + delta + `}]}`
}

func oaiToolCall(id, name, args string) string {
	// arguments arrive split across chunks, as real providers send them
	half := len(args) / 2
	a1, _ := json.Marshal(args[:half])
	a2, _ := json.Marshal(args[half:])
	return strings.Join([]string{
		oaiChunk(`{"tool_calls":[{"index":0,"id":"` + id + `","function":{"name":"` + name + `","arguments":` + string(a1) + `}}]}`),
		oaiChunk(`{"tool_calls":[{"index":0,"function":{"arguments":` + string(a2) + `}}]}`),
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`,
		`data: [DONE]`,
	}, "\n")
}

func oaiText(parts ...string) string {
	var lines []string
	for _, p := range parts {
		b, _ := json.Marshal(p)
		lines = append(lines, oaiChunk(`{"content":`+string(b)+`}`))
	}
	lines = append(lines, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":150,"completion_tokens":5}}`, `data: [DONE]`)
	return strings.Join(lines, "\n")
}

type run struct {
	t      *testing.T
	e      *Engine
	events []claude.Event
}

func startEngine(t *testing.T, provider string, cred Credential, model, mode string) (*run, string) {
	dir := t.TempDir()
	store := NewStore(t.TempDir())
	store.Save(provider, cred)
	e, _ := Start(Options{Cwd: dir, Model: model, Mode: mode, Store: store})
	t.Cleanup(e.Close)
	return &run{t: t, e: e}, dir
}

// until drains events, answering permission prompts with allow, until a
// Result arrives.
func (r *run) until(allow bool) claude.Result {
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.e.Events():
			r.events = append(r.events, ev)
			switch e := ev.(type) {
			case *claude.PermissionRequest:
				if allow {
					r.e.Allow(e, false)
				} else {
					r.e.Deny(e, "no")
				}
			case claude.Result:
				return e
			}
		case <-timeout:
			r.t.Fatal("timed out waiting for result")
		}
	}
}

func (r *run) count(match func(claude.Event) bool) int {
	n := 0
	for _, ev := range r.events {
		if match(ev) {
			n++
		}
	}
	return n
}

func TestOpenAIProtocolToolLoop(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("call_1", "Write", `{"file_path":"hello.txt","content":"hi\n"}`),
		oaiText("Created ", "hello.txt."),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[{"id":"mock-1"},{"id":"models/mock-2"}]}`))
	defer srv.Close()

	r, dir := startEngine(t, "custom", Credential{Key: "k", BaseURL: srv.URL}, "custom/mock-1", "default")
	r.e.Send("make hello.txt")
	res := r.until(true)

	if res.IsError || res.NumTurns != 2 {
		t.Fatalf("result = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hello.txt")); string(b) != "hi\n" {
		t.Fatalf("hello.txt = %q", b)
	}
	if n := r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok }); n != 1 {
		t.Fatalf("permission requests = %d, want 1", n)
	}
	var ready claude.Ready
	for _, ev := range r.events {
		if rd, ok := ev.(claude.Ready); ok {
			ready = rd
		}
	}
	if len(ready.Models) != 2 || ready.Models[1].Value != "custom/mock-2" {
		t.Fatalf("models = %+v", ready.Models)
	}
	// Second request must carry the assistant tool call and its result.
	msgs := s.requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" {
		t.Fatalf("last message = %+v", last)
	}
	if s.requests[0]["messages"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Fatal("system prompt missing")
	}
}

func anthropicSSE(events ...string) string {
	return strings.Join(events, "\n")
}

func TestAnthropicProtocolReadThenEdit(t *testing.T) {
	start := `data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":90}}}`
	stop := `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`
	s := &script{replies: []string{
		anthropicSSE(start,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reading."}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"Read"}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}`,
			`data: {"type":"content_block_stop","index":1}`, stop),
		anthropicSSE(start,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_2","name":"Edit"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"a.go\",\"old_string\":\"old\",\"new_string\":\"new\"}"}}`,
			`data: {"type":"content_block_stop","index":0}`, stop),
		anthropicSSE(start,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[{"id":"claude-x","display_name":"Claude X"}]}`))
	defer srv.Close()

	r, dir := startEngine(t, "anthropic", Credential{Key: "k", BaseURL: srv.URL}, "anthropic/claude-x", "acceptEdits")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a // old\n"), 0o644)
	r.e.Send("rename old to new")
	res := r.until(true)

	if res.IsError || res.NumTurns != 3 {
		t.Fatalf("result = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "package a // new\n" {
		t.Fatalf("a.go = %q", b)
	}
	if n := r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok }); n != 0 {
		t.Fatalf("acceptEdits should not ask, got %d prompts", n)
	}
	// Tool results go back as a user turn with a tool_result block, and the
	// system prompt and newest message carry cache breakpoints.
	req := s.requests[2]
	msgs := req["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	blocks := last["content"].([]any)
	tr := blocks[0].(map[string]any)
	if last["role"] != "user" || tr["type"] != "tool_result" || tr["tool_use_id"] != "tu_2" || tr["cache_control"] == nil {
		t.Fatalf("last message = %+v", last)
	}
	if req["system"].([]any)[0].(map[string]any)["cache_control"] == nil {
		t.Fatal("system prompt not cached")
	}
}

func TestPlanModeRefusesEdits(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("c1", "Write", `{"file_path":"x.txt","content":"x"}`),
		oaiText("Plan: create x.txt."),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, dir := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "plan")
	r.e.Send("do it")
	r.until(true)
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("plan mode wrote a file")
	}
	if !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "Plan mode is read-only") {
		t.Fatal("model was not told why the edit was refused")
	}
}

func TestDenyEndsTurn(t *testing.T) {
	s := &script{replies: []string{oaiToolCall("c1", "Bash", `{"command":"echo hi"}`)}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("run it")
	res := r.until(false)
	if res.IsError || len(s.requests) != 1 {
		t.Fatalf("after deny: result=%+v requests=%d", res, len(s.requests))
	}
}

func TestInterruptStopsCommand(t *testing.T) {
	s := &script{replies: []string{oaiToolCall("c1", "Bash", `{"command":"sleep 30"}`)}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "auto")
	r.e.Send("wait")
	go func() {
		time.Sleep(500 * time.Millisecond)
		r.e.Interrupt()
	}()
	begin := time.Now()
	res := r.until(true)
	if res.Subtype != "error_during_execution" || time.Since(begin) > 5*time.Second {
		t.Fatalf("interrupt: %+v after %s", res, time.Since(begin))
	}
}

func TestGlobRegexp(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "internal/ui/view.go", true},
		{"src/**/*.ts", "src/a/b/c.ts", true},
		{"src/**/*.ts", "src/c.ts", true},
		{"src/*.ts", "src/a/c.ts", false},
		{"*.{js,ts}", "x/y.ts", true},
		{"a,b.txt", "a,b.txt", true},
	}
	for _, c := range cases {
		re, err := globRegexp(c.glob)
		if err != nil || re.MatchString(c.path) != c.want {
			t.Errorf("glob %q on %q: got %v, want %v (err %v)", c.glob, c.path, !c.want, c.want, err)
		}
	}
}

func TestEditRequiresRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a"), 0o644)
	box := NewToolbox(dir)
	if _, err := runEdit(nil, box, map[string]any{"file_path": "f.txt", "old_string": "a", "new_string": "b"}); err == nil {
		t.Fatal("edit without read should fail")
	}
	runRead(nil, box, map[string]any{"file_path": "f.txt"})
	if _, err := runEdit(nil, box, map[string]any{"file_path": "f.txt", "old_string": "a", "new_string": "b"}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderExtrasAreEchoedBack(t *testing.T) {
	sig := `{"google":{"thought_signature":"SIG-123"}}`
	first := strings.Join([]string{
		oaiChunk(`{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"Let me "}]}`),
		oaiChunk(`{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"check.","signature":"RS"}]}`),
		oaiChunk(`{"tool_calls":[{"index":0,"id":"c1","type":"function","extra_content":` + sig + `,"function":{"name":"Glob","arguments":"{\"pattern\":\"*.go\"}"}}]}`),
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n")
	s := &script{replies: []string{first, oaiText("done")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("list go files")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	msgs := s.requests[1]["messages"].([]any)
	var asst map[string]any
	for _, m := range msgs {
		if mm := m.(map[string]any); mm["role"] == "assistant" {
			asst = mm
		}
	}
	call := asst["tool_calls"].([]any)[0].(map[string]any)
	got, _ := json.Marshal(call["extra_content"])
	if string(got) != sig {
		t.Fatalf("extra_content = %s", got)
	}
	rd := asst["reasoning_details"].([]any)
	if len(rd) != 1 || rd[0].(map[string]any)["text"] != "Let me check." || rd[0].(map[string]any)["signature"] != "RS" {
		t.Fatalf("reasoning_details = %v", rd)
	}
}
