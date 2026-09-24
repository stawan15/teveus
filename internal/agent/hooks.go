package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Hooks run the user's own commands around tool calls, in Claude Code's
// format. They live in <config>/hooks.json, globally and per project, never
// in the repository: a repository must not be able to make teveus run
// commands on its own.
//
//	{"hooks": {"PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "gofmt -w ."}]}]},
//	 "projects": {"/path/to/repo": {"hooks": {...}}}}
//
// A hook gets the call as JSON on stdin and runs in the project directory.
// PreToolUse runs before the call: exit code 2 blocks it and its stderr is
// shown to the model. PostToolUse runs after a call that worked: a non-zero
// exit adds the hook's output to the result, so the model sees a failing
// linter or test.

const hookTimeout = 60 * time.Second

type hookCmd struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
}

type hookGroup struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []hookCmd `json:"hooks"`
}

type HookSet map[string][]hookGroup

type hookFile struct {
	Hooks    HookSet `json:"hooks,omitempty"`
	Projects map[string]struct {
		Hooks HookSet `json:"hooks,omitempty"`
	} `json:"projects,omitempty"`
}

type hooks struct{ path, cwd string }

func newHooks(configDir, cwd string) *hooks {
	if configDir == "" {
		return &hooks{cwd: cwd}
	}
	return &hooks{path: filepath.Join(configDir, "hooks.json"), cwd: cwd}
}

// Path is where the hooks file lives ("" when there is no config directory).
func (h *hooks) Path() string { return h.path }

// All returns the hooks that apply here: global ones, then the project's.
func (h *hooks) All() HookSet {
	out := HookSet{}
	if h == nil || h.path == "" {
		return out
	}
	var f hookFile
	if b, err := os.ReadFile(h.path); err == nil {
		json.Unmarshal(b, &f)
	}
	for ev, gs := range f.Hooks {
		out[ev] = append(out[ev], gs...)
	}
	for ev, gs := range f.Projects[h.cwd].Hooks {
		out[ev] = append(out[ev], gs...)
	}
	return out
}

// commands lists the hook commands for an event whose matcher fits the tool.
func (h *hooks) commands(event, tool string) []string {
	var out []string
	for _, g := range h.All()[event] {
		if !matcherFits(g.Matcher, tool) {
			continue
		}
		for _, c := range g.Hooks {
			if (c.Type == "" || c.Type == "command") && strings.TrimSpace(c.Command) != "" {
				out = append(out, c.Command)
			}
		}
	}
	return out
}

func matcherFits(matcher, tool string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(tool)
}

// hookResult is what one hook said.
type hookResult struct {
	block bool   // PreToolUse exit code 2
	out   string // stderr (or stdout) of a hook that blocked or failed
}

// run runs every hook for the event and returns the first that objects.
func (h *hooks) run(ctx context.Context, event, tool string, session string, in map[string]any, response string) hookResult {
	cmds := h.commands(event, tool)
	if len(cmds) == 0 {
		return hookResult{}
	}
	payload := map[string]any{"session_id": session, "cwd": h.cwd, "hook_event_name": event, "tool_name": tool, "tool_input": in}
	if event == "PostToolUse" {
		payload["tool_response"] = response
	}
	stdin, _ := json.Marshal(payload)
	for _, c := range cmds {
		ctx, cancel := context.WithTimeout(ctx, hookTimeout)
		cmd := shellCommand(ctx, c)
		cmd.Dir = h.cwd
		cmd.Env = append(os.Environ(), "TEVEUS_PROJECT_DIR="+h.cwd)
		cmd.Stdin = bytes.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		if err == nil {
			continue
		}
		out := strings.TrimSpace(stderr.String())
		if out == "" {
			out = strings.TrimSpace(stdout.String())
		}
		if out == "" {
			out = err.Error()
		}
		var ee interface{ ExitCode() int }
		blocking := errors.As(err, &ee) && ee.ExitCode() == 2
		if event == "PreToolUse" && !blocking {
			continue // only exit code 2 stops a call
		}
		return hookResult{block: event == "PreToolUse", out: capOutput(out)}
	}
	return hookResult{}
}

// Hooks returns the configured hooks and the file they're read from.
func (e *Engine) Hooks() (HookSet, string) { return e.hooks.All(), e.hooks.Path() }
