package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// engineModel is a Model driving a real agent.Engine, which several commands
// need: /mcp, /hooks, /undo and the saved-permission lists all read its state.
// The engine talks to a server that never answers, so nothing is spent; only
// its local state matters here.
func engineModel(t *testing.T, cwd string) (*Model, *agent.Engine) {
	t.Helper()
	// TEVEUS_CONFIG has to be set before New reads it, or the model picks up
	// whatever the real config dir holds (an approved .mcp.json, say) and the
	// test stops being hermetic.
	cfg := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", cfg)
	dir := t.TempDir()
	store := agent.NewStore(dir)
	store.Save("custom", agent.Credential{BaseURL: "http://127.0.0.1:1"})
	eng, err := agent.Start(agent.Options{
		Cwd: cwd, Model: "custom/m", Mode: "default", Store: store,
		SessionDir: t.TempDir(), ConfigDir: cfg,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	t.Cleanup(eng.Close)

	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.engine, m.client, m.cwd = "api", eng, cwd
	return m, eng
}

func allText(m *Model) string {
	var sb strings.Builder
	for _, b := range m.blocks {
		sb.WriteString(b.text)
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestShowMCPOnTheAPiEngineListsServers(t *testing.T) {
	m, _ := engineModel(t, t.TempDir())
	m.showMCP()
	text := allText(m)
	if !strings.Contains(text, "MCP servers") {
		t.Fatalf("/mcp on the API engine = %q", text)
	}
	if !strings.Contains(text, "mcp.json") {
		t.Fatalf("it doesn't say where to add servers: %q", text)
	}
}

func TestShowMCPOnClaudeCodeGoesToTheCLI(t *testing.T) {
	// The CLI runs its own servers, so /mcp is forwarded as a prompt.
	m := testModel(t)
	m.engine = "claude"
	b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event)}}
	m.client = b
	m.showMCP()
	if len(b.sent) != 1 || b.sent[0] != "/mcp" {
		t.Fatalf("sent %v, want the command forwarded", b.sent)
	}
}

func TestShowMCPOffersToStartAProjectsServers(t *testing.T) {
	// A project .mcp.json needs approval before its programs run.
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, ".mcp.json"), []byte(`{"mcpServers":{"notes":{"command":"echo","args":["hi"]}}}`), 0o644)

	m, eng := engineModel(t, cwd)
	// The engine scans for MCP servers in the background as it starts; the
	// picker only appears once it has seen this project's file.
	waitUntil(t, func() bool {
		for _, s := range eng.MCPStatus() {
			if s.State == "needs approval" {
				return true
			}
		}
		return false
	})
	m.showMCP()
	if m.pop.mode != popPicker {
		t.Fatalf("no approval picker: %v (status %+v)", m.pop.mode, eng.MCPStatus())
	}
	if len(m.pop.list) < 2 {
		t.Fatalf("picker = %+v", m.pop.list)
	}
	// Choosing "Not now" leaves everything alone.
	m.pop.list[1].run(m)
	if m.blocks[len(m.blocks)-1].text == "" {
		t.Fatal("nothing shown")
	}
}

func TestShowHooksListsConfiguredHooks(t *testing.T) {
	// A hook file in the config dir is read and shown.
	cwd, cfg := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(cfg, "hooks.json"),
		[]byte(`{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo done"}]}]}}`), 0o600)

	store := agent.NewStore(t.TempDir())
	eng, err := agent.Start(agent.Options{Cwd: cwd, Model: "custom/m", Store: store,
		SessionDir: t.TempDir(), ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)

	t.Setenv("TEVEUS_CONFIG", cfg)
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.engine, m.client, m.cwd = "api", eng, cwd

	m.showHooks()
	text := allText(m)
	if !strings.Contains(strings.ToLower(text), "hook") {
		t.Fatalf("/hooks = %q", text)
	}
}

func TestHandleMCPApprovedReportsTheResult(t *testing.T) {
	m := testModel(t)
	m.handleMCPApproved(mcpApprovedMsg{err: os.ErrPermission})
	if !strings.Contains(allText(m), "permission") {
		t.Fatalf("a failed approval isn't reported: %q", allText(m))
	}
	m.handleMCPApproved(mcpApprovedMsg{}) // no error: says it started
	if !strings.Contains(strings.ToLower(allText(m)), "mcp") {
		t.Fatalf("success isn't reported: %q", allText(m))
	}
}

func TestNoteProjectMCPMentionsApproval(t *testing.T) {
	// A project .mcp.json is called out once, so its programs don't run unseen.
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, ".mcp.json"),
		[]byte(`{"mcpServers":{"notes":{"command":"echo","args":["hi"]}}}`), 0o644)
	m, _ := engineModel(t, cwd)
	m.blocks = nil
	m.noteProjectMCP()
	if !strings.Contains(allText(m), "notes") {
		t.Fatalf("the server wasn't named: %q", allText(m))
	}
	// It only says it once.
	m.blocks = nil
	m.noteProjectMCP()
	if len(m.blocks) != 0 {
		t.Fatal("the note repeated")
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	m, _ := engineModel(t, t.TempDir())
	m.undo()
	if !strings.Contains(strings.ToLower(m.notice), "nothing") {
		t.Fatalf("notice = %q", m.notice)
	}
	// And it says where it works at all.
	cm := testModel(t)
	cm.engine = "claude"
	cm.client = &tabBackend{ch: make(chan claude.Event)}
	cm.undo()
	if !strings.Contains(cm.notice, "/undo works on the Direct API engine") {
		t.Fatalf("claude-code message = %q", cm.notice)
	}
}

func TestShowPermissionsWithNoRulesSaysHowToAddOne(t *testing.T) {
	m, _ := engineModel(t, t.TempDir())
	m.showPermissions("")
	text := allText(m)
	if !strings.Contains(text, "Permission rules") || !strings.Contains(text, "None yet") {
		t.Fatalf("/permissions = %q", text)
	}
	if m.pop.open() {
		t.Fatal("an empty rule list opened a picker")
	}
}

func TestShowPermissionsAddsAndRemovesARule(t *testing.T) {
	m, eng := engineModel(t, t.TempDir())

	// A rule needs a body.
	m.showPermissions("allow")
	if !strings.Contains(m.notice, "usage") {
		t.Fatalf("notice = %q", m.notice)
	}

	m.showPermissions("allow Bash(go test:*)")
	if len(eng.PermissionRules().Allow) == 0 {
		t.Fatal("the rule wasn't saved")
	}
	// With rules present the list opens a picker to remove one.
	m.showPermissions("")
	if m.pop.mode != popPicker || len(m.pop.list) == 0 {
		t.Fatalf("popup = %v with %d rows", m.pop.mode, len(m.pop.list))
	}
	m.pop.list[0].run(m)
	if n := len(eng.PermissionRules().Allow); n != 0 {
		t.Fatalf("%d rules left after removing one", n)
	}

	// And a deny rule goes in its own list.
	m.showPermissions("deny WebFetch")
	if len(eng.PermissionRules().Deny) == 0 {
		t.Fatal("the deny rule wasn't saved")
	}
}

func TestShowPermissionsOnClaudeCodeForwardsTheCommand(t *testing.T) {
	m := testModel(t)
	m.engine = "claude"
	b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event)}}
	m.client = b
	m.showPermissions("allow Bash(ls:*)")
	if len(b.sent) != 1 || !strings.Contains(b.sent[0], "/permissions") {
		t.Fatalf("sent %v", b.sent)
	}
}

func TestHeadlessReportsWhenNothingIsConnected(t *testing.T) {
	// -p with no engine returns an explanation instead of hanging.
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	var out, errOut strings.Builder
	cfg := Config{Headless: true, Dark: true, Engine: ""}
	err := RunHeadless(cfg, "say hi", false, &out, &errOut)
	if err == nil {
		t.Fatalf("no error with nothing connected (stdout %q)", out.String())
	}
	if !strings.Contains(err.Error(), "engine") {
		t.Fatalf("error doesn't say how to fix it: %v", err)
	}
}

func TestHeadlessNotesTheClaudeCodeTerms(t *testing.T) {
	// On the Claude engine it says what it is about to run, once.
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	var out, errOut strings.Builder
	cfg := Config{Headless: true, Dark: true, Engine: "claude"}
	_ = RunHeadless(cfg, "say hi", false, &out, &errOut)
	if !strings.Contains(errOut.String(), "note:") {
		t.Fatalf("no notice on stderr: %q", errOut.String())
	}
}

func TestMigrateOldConfigMovesThePreviousName(t *testing.T) {
	base := t.TempDir()
	old, dir := filepath.Join(base, "claude-tui"), filepath.Join(base, "teveus")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "settings.json"), []byte(`{"theme":"dark"}`), 0o644)

	migrateOldConfig(old, dir)
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatalf("settings weren't moved: %v", err)
	}
	// It never overwrites a folder that already exists.
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "old.json"), []byte("{}"), 0o644)
	migrateOldConfig(other, dir)
	if _, err := os.Stat(filepath.Join(dir, "old.json")); err == nil {
		t.Fatal("an existing config dir was overwritten")
	}
}

func TestTightenConfigFixesPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", dir)
	for _, n := range []string{"settings.json", "history.json"} {
		os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o644)
	}
	tightenConfig()
	for _, n := range []string{"settings.json", "history.json"} {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s = %o, want 600", n, perm)
		}
	}
}

func TestSidebarAndStatusSurviveEveryWidth(t *testing.T) {
	// A narrow terminal must not lose the sidebar's content or panic.
	m := testModel(t)
	m.settings.HideSide = false
	m.add(&block{kind: kindUser, text: "hello"})
	m.settings.Effort = "max"
	for _, w := range []int{40, 60, 80, 100, 120, 200} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		if frame := m.View(); frame == "" {
			t.Fatalf("no frame at width %d", w)
		}
		side := ansi.Strip(m.sidebar(20))
		for _, line := range strings.Split(side, "\n") {
			if n := len([]rune(line)); n > 0 && n > w {
				t.Fatalf("sidebar line wider than the window at %d: %q", w, line)
			}
		}
	}
}

func TestRenderersHandleEveryBlockKind(t *testing.T) {
	m := testModel(t)
	kinds := []blockKind{kindUser, kindAssistant, kindTool, kindInfo, kindError, kindTurnEnd, kindCmdOut}
	for _, k := range kinds {
		b := &block{kind: k, text: "sample text", name: "Bash", state: toolFailed}
		m.add(b)
	}
	m.refresh()
	if len(m.lines) == 0 {
		t.Fatal("nothing rendered")
	}
	frame := ansi.Strip(m.View())
	if !strings.Contains(frame, "sample text") {
		t.Fatalf("the blocks didn't reach the frame:\n%s", frame)
	}
}

// waitUntil polls cond for a few seconds, for work the engine does in the
// background.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the engine")
}
