package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

// TestHandleEventCoversEveryEventType drives the transcript with one of each
// event the backends emit. The UI is a state machine over these events, so
// this is the widest single test of it: each event must land somewhere rather
// than be silently dropped.
func TestHandleEventCoversEveryEventType(t *testing.T) {
	m := testModel(t)
	m.client = &tabBackend{ch: make(chan claude.Event, 8)}

	cases := []struct {
		name  string
		event claude.Event
		check func(*testing.T)
	}{
		{"ready", claude.Ready{Models: []claude.ModelInfo{{Value: "openai/gpt-5.4", DisplayName: "gpt-5.4"}},
			PermissionMode: "plan"}, func(t *testing.T) {
			if len(m.models) != 1 || m.mode != "plan" {
				t.Fatalf("models=%d mode=%q", len(m.models), m.mode)
			}
		}},
		{"init", claude.Init{SessionID: "api-1", Model: "openai/gpt-5.4", Cwd: m.cwd, PermissionMode: "default"},
			func(t *testing.T) {
				if m.sessionID != "api-1" {
					t.Fatalf("session = %q", m.sessionID)
				}
			}},
		{"status compacting", claude.Status{Status: "compacting"}, func(t *testing.T) {
			if !strings.Contains(m.phase, "Compacting") {
				t.Fatalf("phase = %q", m.phase)
			}
		}},
		{"status retrying", claude.Status{Status: "retrying in 4s (busy)"}, func(t *testing.T) {
			if !strings.Contains(m.phase, "Provider busy") {
				t.Fatalf("phase = %q", m.phase)
			}
		}},
		{"notice", claude.Notice{Text: "effort lowered"}, func(t *testing.T) {
			if !strings.Contains(allText(m), "effort lowered") {
				t.Fatal("the notice wasn't shown")
			}
		}},
		{"user message", claude.UserMessage{Text: "said mid-turn"}, func(t *testing.T) {
			if !strings.Contains(allText(m), "said mid-turn") {
				t.Fatal("the mid-turn message wasn't shown")
			}
		}},
		{"compacted", claude.Compacted{Auto: true, PreTokens: 120000}, func(t *testing.T) {
			if !strings.Contains(allText(m), "compacted") {
				t.Fatal("compaction wasn't reported")
			}
		}},
		{"block start text", claude.BlockStart{Kind: "text"}, nil},
		{"block start thinking", claude.BlockStart{Kind: "thinking"}, nil},
		{"block start tool", claude.BlockStart{Kind: "tool_use", ToolName: "Bash"}, nil},
		{"text delta", claude.TextDelta{Text: "streamed"}, func(t *testing.T) {
			if !strings.Contains(allText(m), "streamed") {
				t.Fatal("streamed text was dropped")
			}
		}},
		{"assistant text", claude.AssistantMessage{Blocks: []claude.ContentBlock{
			{Type: "text", Text: "a whole reply"}}}, func(t *testing.T) {
			if !strings.Contains(allText(m), "a whole reply") {
				t.Fatal("the reply was dropped")
			}
		}},
		{"assistant tool call", claude.AssistantMessage{Blocks: []claude.ContentBlock{
			{Type: "tool_use", ID: "tu-1", Name: "Bash",
				Input: json.RawMessage(`{"command":"echo hi"}`)}}}, func(t *testing.T) {
			if len(m.tools) == 0 {
				t.Fatal("the tool call wasn't recorded")
			}
		}},
		{"tool results", claude.ToolResults{Results: []claude.ContentBlock{
			{Type: "tool_result", ToolUseID: "tu-1", Content: []byte(`"hi"`)}}}, nil},
		{"rate limit", claude.RateLimit{Status: "ok",
			FiveHour: &claude.Window{Utilization: 0.5},
			SevenDay: &claude.Window{Utilization: 0.1},
			Monthly:  &claude.Window{Utilization: 0.02}}, func(t *testing.T) {
			if m.fiveHour == nil || m.sevenDay == nil || m.monthly == nil {
				t.Fatal("usage windows weren't kept")
			}
		}},
		{"result", claude.Result{Subtype: "success", CostUSD: 0.5,
			Usage: claude.Usage{InputTokens: 10, OutputTokens: 5}}, func(t *testing.T) {
			if m.busy {
				t.Fatal("the turn never finished")
			}
			if m.cost != 0.5 {
				t.Fatalf("cost = %v", m.cost)
			}
		}},
		{"result error", claude.Result{Subtype: "error_max_turns", IsError: true}, func(t *testing.T) {
			if !strings.Contains(allText(m), "max turns") {
				t.Fatalf("the failure wasn't shown: %q", allText(m))
			}
		}},
		{"unknown", claude.Unknown{Line: `{"type":"something_new"}`}, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m.handleEvent(c.event)
			m.layout()
			m.refresh()
			if c.check != nil {
				c.check(t)
			}
		})
	}
}

func TestPermissionRequestWaitsForAnAnswer(t *testing.T) {
	m := testModel(t)
	req := &claude.PermissionRequest{RequestID: "r1", ToolName: "Bash",
		Input: json.RawMessage(`{"command":"ls"}`)}
	m.handleEvent(req)
	if len(m.perms) != 1 {
		t.Fatalf("%d prompts waiting", len(m.perms))
	}
	// The status bar says so, and the keys are offered.
	if !strings.Contains(ansiStripped(m.statusBar()), "approval") {
		t.Fatalf("status bar = %q", ansiStripped(m.statusBar()))
	}

	// "a" only works when the CLI offered a rule to save.
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if len(m.perms) != 1 {
		t.Fatal("'a' answered a prompt with no suggestions")
	}
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if len(m.perms) != 0 {
		t.Fatal("'n' didn't deny the tool")
	}
}

func TestResultWithQueuedWorkStartsTheNextTurn(t *testing.T) {
	m := testModel(t)
	b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event, 8)}}
	m.client = b
	m.busy = true
	m.queue = []string{"second task"}

	m.handleEvent(claude.Result{Subtype: "success"})
	if len(m.queue) != 0 {
		t.Fatalf("queue = %v", m.queue)
	}
	if len(b.sent) != 1 || b.sent[0] != "second task" {
		t.Fatalf("sent %v", b.sent)
	}
}

func TestTodoToolUpdatesTheTaskList(t *testing.T) {
	m := testModel(t)
	// A TodoWrite call replaces the list, which the sidebar draws.
	m.handleEvent(claude.AssistantMessage{Blocks: []claude.ContentBlock{
		{Type: "tool_use", ID: "t1", Name: "TodoWrite",
			Input: json.RawMessage(`{"todos":[{"content":"write tests","status":"in_progress"}]}`)}}})
	m.layout()
	if len(m.todos) == 0 && len(m.tasks) == 0 {
		t.Fatal("the todo list wasn't recorded")
	}
	frame := ansiStripped(m.View())
	if frame == "" {
		t.Fatal("no frame")
	}
}

func TestNewSessionKeepsTheOldOneRunning(t *testing.T) {
	m := testModel(t)
	m.add(&block{kind: kindUser, text: "first work"})
	m.newSession()
	if len(m.sessions) != 2 {
		t.Fatalf("%d sessions", len(m.sessions))
	}
	if len(m.blocks) != 0 {
		t.Fatal("the new session carried the old transcript")
	}
	// Switching back finds the first conversation intact.
	m.switchTo(0)
	if len(m.blocks) == 0 || !strings.Contains(allText(m), "first work") {
		t.Fatal("the first session lost its transcript")
	}
}

func TestCloseSessionDropsOnlyThatOne(t *testing.T) {
	m := testModel(t)
	m.newSession()
	m.add(&block{kind: kindUser, text: "second work"})
	m.closeSession()
	if len(m.sessions) != 1 {
		t.Fatalf("%d sessions left", len(m.sessions))
	}
	if m.cur >= len(m.sessions) {
		t.Fatalf("cur = %d with %d sessions", m.cur, len(m.sessions))
	}
}

func TestSessionTitlesComeFromTheFirstMessage(t *testing.T) {
	m := testModel(t)
	m.add(&block{kind: kindUser, text: "refactor the parser please"})
	if got := sessionTitle(m.blocks); !strings.Contains(got, "refactor") {
		t.Fatalf("title = %q", got)
	}
	if got := sessionTitle(nil); got != "New session" {
		t.Fatalf("empty title = %q", got)
	}
	// A very long first message is shortened.
	m2 := testModel(t)
	m2.add(&block{kind: kindUser, text: strings.Repeat("long ", 40)})
	if n := len([]rune(sessionTitle(m2.blocks))); n > 45 {
		t.Fatalf("title is %d runes", n)
	}
}

// ansiStripped removes colour so a test can look for words.
func ansiStripped(s string) string {
	return strings.TrimSpace(ansi.Strip(s))
}

func TestResultCountsTokensAndEndsTheTurn(t *testing.T) {
	m := testModel(t)
	m.engine = "api"
	m.busy = true
	m.turnStart = time.Now()
	m.handleEvent(claude.Result{Subtype: "success", CostUSD: 0,
		DurationMS: 1500, NumTurns: 2,
		Usage: claude.Usage{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 400}})

	// Without a reported cost the footer shows tokens, which is the honest
	// measure on a subscription or a free model.
	if m.tokIn != 500 || m.tokOut != 20 {
		t.Fatalf("tokens = %d in / %d out", m.tokIn, m.tokOut)
	}
	if m.busy {
		t.Fatal("the turn never ended")
	}
	text := allText(m)
	if !strings.Contains(text, "Worked for") || !strings.Contains(text, "500 in") {
		t.Fatalf("footer = %q", text)
	}
}
