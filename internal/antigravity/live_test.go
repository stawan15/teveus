package antigravity

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// The client talks to the real CLI when one is installed. The rest of the
// package is covered without it, so a machine with no agy still runs the suite.
func liveClient(t *testing.T, mode string) *Client {
	t.Helper()
	bin, err := exec.LookPath("agy")
	if err != nil {
		t.Skip("agy is not installed")
	}
	c, err := Start(Options{Binary: bin, Cwd: t.TempDir(), PermissionMode: mode, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// collect drains events until a Result arrives or the deadline passes.
func collect(t *testing.T, c *Client, allow bool, d time.Duration) ([]claude.Event, claude.Result) {
	t.Helper()
	var seen []claude.Event
	var res claude.Result
	timeout := time.After(d)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				return seen, res
			}
			seen = append(seen, ev)
			switch e := ev.(type) {
			case *claude.PermissionRequest:
				if allow {
					c.Allow(e, false)
				} else {
					c.Deny(e, "no")
				}
			case claude.Result:
				return seen, e
			}
		case <-timeout:
			return seen, res
		}
	}
}

func TestLiveHandshake(t *testing.T) {
	c := liveClient(t, "default")
	var ready claude.Ready
	var init claude.Init
	timeout := time.After(20 * time.Second)
	for init.SessionID == "" {
		select {
		case ev := <-c.Events():
			switch e := ev.(type) {
			case claude.Ready:
				ready = e
			case claude.Init:
				init = e
			case claude.Exited:
				t.Fatalf("the CLI exited: %v %s", e.Err, e.Stderr)
			}
		case <-timeout:
			t.Fatal("no handshake")
		}
	}
	if init.SessionID == "" {
		t.Error("no session id")
	}
	if init.Model == "" {
		t.Error("no initial model reported")
	}
	if len(ready.Models) == 0 {
		t.Error("no models offered")
	} else {
		t.Logf("%d models, first %q (group=%q meta=%q) active=%q", len(ready.Models), ready.Models[0].Value, ready.Models[0].Group, ready.Models[0].Meta, init.Model)
	}
	if err := c.SetEffort("low"); err != nil {
		t.Errorf("SetEffort(low): %v", err)
	}
}

// A turn streams text and ends with a Result carrying the token counts.
func TestLiveTurn(t *testing.T) {
	c := liveClient(t, "default")
	if err := c.Send("Reply with exactly: ok"); err != nil {
		t.Fatal(err)
	}
	seen, res := collect(t, c, true, 90*time.Second)
	var text strings.Builder
	for _, ev := range seen {
		if d, ok := ev.(claude.TextDelta); ok {
			text.WriteString(d.Text)
		}
	}
	if res.Subtype == "" {
		t.Fatalf("no result; text=%q events=%d", text.String(), len(seen))
	}
	if !strings.Contains(strings.ToLower(text.String()), "ok") {
		t.Errorf("text = %q", text.String())
	}
	if res.Usage.InputTokens == 0 {
		t.Error("no token counts")
	}
	sess, err := agent.LoadSession(c.sessionDir, c.session)
	if err != nil {
		t.Errorf("session not saved: %v", err)
	} else if sess.Engine != "antigravity" || len(sess.History) < 2 {
		t.Errorf("saved session = %+v", sess)
	}
	t.Logf("input=%d output=%d text=%q", res.Usage.InputTokens, res.Usage.OutputTokens, text.String())
}

// A tool call reaches the UI as a permission request, and denying it stops the
// work: the whole point of not using the headless mode.
func TestLivePermissionAsksAndDenyStops(t *testing.T) {
	c := liveClient(t, "default")
	dir := c.cmd.Dir
	path := dir + "/denied.txt"
	os.Remove(path)

	if err := c.Send("Create a file named denied.txt containing hi"); err != nil {
		t.Fatal(err)
	}
	seen, res := collect(t, c, false, 120*time.Second)
	asked := false
	for _, ev := range seen {
		if _, ok := ev.(*claude.PermissionRequest); ok {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("the CLI never asked before writing; events=%d result=%+v", len(seen), res)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the file was written after a denial")
	}
}

// The UI's tool names and arguments are what the renderer expects.
func TestToolMapping(t *testing.T) {
	for cliName, want := range map[string]string{
		"view_file":                  "Read",
		"write_to_file":              "Write",
		"replace_file_content":       "Edit",
		"multi_replace_file_content": "Edit",
		"run_command":                "Bash",
		"grep_search":                "Grep",
		"find_by_name":               "Glob",
		"read_url_content":           "WebFetch",
		"invoke_subagent":            "Task",
		"some_new_tool":              "some_new_tool", // unknown names survive
	} {
		if got := mapToolName(cliName); got != want {
			t.Errorf("%s -> %s, want %s", cliName, got, want)
		}
	}
}

func TestToolInputIsNormalized(t *testing.T) {
	cases := []struct {
		tool, raw, key, want string
	}{
		{"Read", `{"AbsolutePath":"/a/b.go"}`, "file_path", "/a/b.go"},
		{"Bash", `{"CommandLine":"go test ./...","Cwd":"/x"}`, "command", "go test ./..."},
		{"Write", `{"Path":"/a","Content":"hi"}`, "file_path", "/a"},
		{"Edit", `{"Path":"/a","TargetContent":"old","ReplacementContent":"new"}`, "old_string", "old"},
		{"Grep", `{"Query":"func main","SearchPath":"/p"}`, "pattern", "func main"},
	}
	for _, tc := range cases {
		out := normalizeToolInput(tc.tool, []byte(tc.raw))
		if !strings.Contains(string(out), `"`+tc.key+`"`) {
			t.Errorf("%s: %s has no %q", tc.tool, out, tc.key)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: %s lost %q", tc.tool, out, tc.want)
		}
	}
}

func TestPermissionDescription(t *testing.T) {
	if got := permissionDescription("", "Bash", []byte(`{"CommandLine":"rm -rf /tmp/x"}`)); !strings.Contains(got, "rm -rf") {
		t.Errorf("bash: %q", got)
	}
	if got := permissionDescription("", "Write", []byte(`{"Path":"/a/b.txt"}`)); !strings.Contains(got, "/a/b.txt") {
		t.Errorf("write: %q", got)
	}
	if got := permissionDescription("Do a thing", "Mystery", []byte(`{}`)); got != "Do a thing" {
		t.Errorf("fallback to title: %q", got)
	}
}

func TestModesMapToTheCLIs(t *testing.T) {
	for in, want := range map[string]string{
		"default": "default", "acceptEdits": "accept-edits", "auto": "auto",
		"bypassPermissions": "auto", "plan": "plan", "": "default",
	} {
		if got := cliMode(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if !modeAllowsAll("auto") || modeAllowsAll("acceptEdits") {
		t.Error("auto should answer itself; accept-edits should still ask for commands")
	}
}

func TestParseSessionModelsAndClampEffort(t *testing.T) {
	cfgOpts := []configOption{
		{
			ID: "model", CurrentValue: "gemini-4-neon",
			Options: []struct {
				Value string `json:"value"`
				Name  string `json:"name"`
			}{
				{Value: "gemini-4-neon", Name: "Gemini 4 Neon"},
				{Value: "gemini-3.8-flash", Name: "Gemini 3.8 Flash"},
				{Value: "claude-opus-5-5", Name: "Claude Opus 5.5"},
				{Value: "gemini-pro-orchestrator", Name: "Orchestrator (Carbon)"},
			},
		},
		{
			ID: "thought_level", CurrentValue: "high",
			Options: []struct {
				Value string `json:"value"`
				Name  string `json:"name"`
			}{
				{Value: "low", Name: "Low"},
				{Value: "medium", Name: "Medium"},
				{Value: "high", Name: "High"},
				{Value: "xhigh", Name: "Extra"},
			},
		},
	}
	avail := []availableModel{
		{ModelID: "gemini-4-neon-low"}, {ModelID: "gemini-4-neon-medium"}, {ModelID: "gemini-4-neon-high"}, {ModelID: "gemini-4-neon-xhigh"},
		{ModelID: "gemini-3.8-flash-low"}, {ModelID: "gemini-3.8-flash-medium"}, {ModelID: "gemini-3.8-flash-high"},
		{ModelID: "claude-opus-5-5-low"}, {ModelID: "claude-opus-5-5-high"},
	}
	models, cur, levels, ok := parseSessionModels(cfgOpts, avail)
	if !ok || cur != "gemini-4-neon" || len(models) != 4 || len(levels) != 4 {
		t.Fatalf("parseSessionModels: ok=%v cur=%q models=%d levels=%v", ok, cur, len(models), levels)
	}
	if models[0].Group != "Antigravity · Gemini 4" || models[0].Meta != "low–xhigh" || len(models[0].Reasoning) != 4 {
		t.Errorf("neon: %+v", models[0])
	}
	if models[1].Group != "Antigravity · Gemini" || models[1].Meta != "low–high" || len(models[1].Reasoning) != 3 {
		t.Errorf("flash: %+v", models[1])
	}
	if models[2].Group != "Antigravity · Claude" {
		t.Errorf("claude group: %+v", models[2])
	}
	if models[3].Group != "Antigravity · Orchestrator" {
		t.Errorf("orchestrator group: %+v", models[3])
	}
	if got := clampThoughtLevel("max", []string{"low", "medium", "high"}); got != "high" {
		t.Errorf("clamp max -> %q, want high", got)
	}
	if got := clampThoughtLevel("max", []string{"low", "medium", "high", "xhigh"}); got != "xhigh" {
		t.Errorf("clamp max -> %q, want xhigh", got)
	}
	if got := clampThoughtLevel("medium", []string{"low", "medium", "high"}); got != "medium" {
		t.Errorf("clamp medium -> %q, want medium", got)
	}
	if got := stopReasonSubtype("end_turn"); got != "success" {
		t.Errorf("end_turn -> %q, want success", got)
	}
	if got := stopReasonSubtype("cancelled"); got != "error_during_execution" {
		t.Errorf("cancelled -> %q, want error_during_execution", got)
	}
}
