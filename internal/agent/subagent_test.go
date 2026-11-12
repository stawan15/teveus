package agent

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

// A Task call must run a subagent with read-only tools, nest its events under
// the Task call's ID, and put its report in the tool result the main model
// reads.
func TestTaskRunsASubagent(t *testing.T) {
	s := &script{replies: []string{
		// The main model asks for a subagent.
		oaiToolCall("call-1", "Task", `{"description":"find the parser","prompt":"find the parser code"}`),
		// The subagent searches, then reports.
		oaiToolCall("sub-1", "Glob", `{"pattern":"**/parser*.go"}`),
		oaiText("The parser lives in parser.go."),
		// The main model answers with what the subagent found.
		oaiText("It is in parser.go."),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()

	r, dir := startEngine(t, "openai", Credential{Key: "k", BaseURL: srv.URL}, "openai/m", "auto")
	writeFile(t, filepath.Join(dir, "parser.go"), "package p\n")
	r.e.Send("where is the parser?")
	res := r.until(true)
	if res.IsError {
		t.Fatalf("turn failed: %s", res.Text)
	}

	// The subagent's Glob must be nested under the Task call.
	nested := 0
	for _, ev := range r.events {
		if am, ok := ev.(claude.AssistantMessage); ok && am.Parent == "call-1" {
			for _, b := range am.Blocks {
				if b.Type == "tool_use" && b.Name == "Glob" {
					nested++
				}
			}
		}
	}
	if nested != 1 {
		t.Fatalf("subagent's Glob was not nested under the Task call: %d", nested)
	}

	// The report must come back as the Task tool's result.
	report := ""
	for _, ev := range r.events {
		if tr, ok := ev.(claude.ToolResults); ok && tr.Parent == "" {
			for _, b := range tr.Results {
				if b.ToolUseID == "call-1" {
					report = b.ResultText()
				}
			}
		}
	}
	if !strings.Contains(report, "parser.go") {
		t.Fatalf("the subagent's report did not reach the main model: %q", report)
	}
}

// A subagent must not be able to change anything: no Write, no Edit, no shell.
func TestSubagentGetsOnlyReadOnlyTools(t *testing.T) {
	e, _ := Start(Options{Cwd: t.TempDir(), Model: "openai/m", Mode: "auto",
		Store: NewStore(t.TempDir())})
	defer e.Close()
	names := map[string]bool{}
	for _, tl := range e.subagentTools() {
		names[tl.def.Name] = true
	}
	for _, forbidden := range []string{"Write", "Edit", "Bash", "Task", "NotebookEdit"} {
		if names[forbidden] {
			t.Errorf("subagent was given %s", forbidden)
		}
	}
	for _, want := range []string{"Read", "Grep", "Glob"} {
		if !names[want] {
			t.Errorf("subagent is missing %s", want)
		}
	}
}

// The SubagentModel setting must pick the model the subagent runs on, falling
// back to the conversation's when that model isn't connected.
func TestSubagentModelFallback(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("call-1", "Task", `{"description":"look","prompt":"look around"}`),
		oaiText("nothing here."),
		oaiText("done."),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()

	r, _ := startEngine(t, "openai", Credential{Key: "k", BaseURL: srv.URL}, "openai/main-model", "auto")
	// Ask for a provider that isn't connected: it must not fail the turn.
	r.e.SetSubagentModel("groq/not-connected")
	r.e.Send("look around and report")
	res := r.until(true)
	if res.IsError {
		t.Fatalf("a missing subagent model failed the turn: %s", res.Text)
	}
	for _, req := range s.requests {
		if m, _ := req["model"].(string); m == "not-connected" {
			t.Fatal("the unconnected model was used anyway")
		}
	}
}
