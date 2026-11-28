package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/stawan15/teveus/internal/claude"
)

func TestShimmerUpdatesOnThemeSwitch(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { applyTheme(themes[0]) })

	applyTheme(themes[0]) // claude
	first := shimmerShades[0]
	gruv, ok := themeByName("gruvbox")
	if !ok {
		t.Fatal("gruvbox theme missing")
	}
	applyTheme(gruv)
	second := shimmerShades[0]
	if first == second {
		t.Fatalf("shimmerShades did not update on theme switch: %q", first)
	}
}

func TestReadChainFoldsAndExpandsOnClick(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	b1 := &block{kind: kindTool, name: "Read", input: map[string]any{"file_path": "/tmp/a.go"}, result: "line1\nline2\n", state: toolDone}
	b2 := &block{kind: kindTool, name: "Read", input: map[string]any{"file_path": "/tmp/b.go"}, result: "line1\n", state: toolDone}
	m.add(b1)
	m.add(b2)
	m.refresh()

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Read ×2") {
		t.Fatalf("expected folded read chain in transcript:\n%s", view)
	}

	// Clicking the folded row expands both blocks in the chain.
	x, y := find(t, m, "Read ×2")
	mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, x, y)
	mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, x, y)
	if !b1.open || !b2.open {
		t.Fatalf("clicking folded read chain should open all blocks: b1=%v b2=%v", b1.open, b2.open)
	}
	expanded := ansi.Strip(m.View())
	if strings.Contains(expanded, "Read ×2") {
		t.Fatalf("chain stayed folded after click:\n%s", expanded)
	}
}

func TestInlineDiffHighlightsChangedSpan(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])
	lines := diffLines("return sum * discount", "return sum * (1 - discount)", 60)
	if len(lines) != 2 {
		t.Fatalf("expected 2 diff lines, got %d", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 60 {
			t.Fatalf("diff line %d overflowed width 60: got %d (%q)", i, w, ansi.Strip(l))
		}
		if !strings.Contains(l, "\x1b[") {
			t.Fatalf("diff line %d missing styling", i)
		}
	}
	if !strings.Contains(ansi.Strip(lines[0]), "- return sum * discount") ||
		!strings.Contains(ansi.Strip(lines[1]), "+ return sum * (1 - discount)") {
		t.Fatalf("unexpected diff text:\n%s\n%s", ansi.Strip(lines[0]), ansi.Strip(lines[1]))
	}
}

func TestSmartApprovalAvoidsDuplicateDiff(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 118, Height: 34})
	edit := &block{
		kind:  kindTool,
		id:    "t4",
		name:  "Edit",
		state: toolRunning,
		input: map[string]any{
			"file_path":  "/tmp/cart.go",
			"old_string": "return sum * discount",
			"new_string": "return sum * (1 - discount)",
		},
	}
	m.tools["t4"] = edit
	m.add(edit)
	m.refresh()

	in, _ := json.Marshal(edit.input)
	m.handleEvent(&claude.PermissionRequest{
		RequestID: "p1", ToolName: "Edit", ToolUseID: "t4", Description: "cart.go", Input: in,
	})
	m.layout()

	compact := ansi.Strip(m.permView())
	if !strings.Contains(compact, "diff highlighted above") {
		t.Fatalf("expected compact approval bar when diff is visible above:\n%s", compact)
	}
	if strings.Contains(compact, "return sum * discount") {
		t.Fatalf("compact approval bar duplicated diff text:\n%s", compact)
	}

	// Pressing 'd' expands the full diff inside permView.
	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	full := ansi.Strip(m.permView())
	if !strings.Contains(full, "return sum * discount") {
		t.Fatalf("pressing 'd' did not expand full diff:\n%s", full)
	}
}

func TestSaverPresetsApplyAndReport(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	if got := m.saverPresetName(); got != "full power" {
		t.Fatalf("default preset = %q, want full power", got)
	}
	m.applySaverPreset("balanced")
	if !m.settings.Concise || !m.settings.LeanTools || m.minimalLevel() != "lite" {
		t.Fatalf("balanced preset mismatch: concise=%v lean=%v minimal=%q",
			m.settings.Concise, m.settings.LeanTools, m.minimalLevel())
	}
	if got := m.saverPresetName(); got != "balanced" {
		t.Fatalf("preset = %q, want balanced", got)
	}
	m.applySaverPreset("max")
	if m.minimalLevel() != "strict" || m.saverPresetName() != "max saver" {
		t.Fatalf("max preset mismatch: minimal=%q name=%q", m.minimalLevel(), m.saverPresetName())
	}
	m.applySaverPreset("full")
	if m.settings.Concise || m.settings.LeanTools || m.minimalLevel() != "off" {
		t.Fatalf("full preset mismatch: concise=%v lean=%v minimal=%q",
			m.settings.Concise, m.settings.LeanTools, m.minimalLevel())
	}
}

func TestSubagentLiveTelemetryAndRail(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.engine, m.model = "claude", "claude-haiku-4-5-20251001"
	m.Update(tea.WindowSizeMsg{Width: 132, Height: 38})
	m.busy, m.phase = true, "Running Task"

	taskIn, _ := json.Marshal(map[string]any{"description": "Inspect auth flow", "prompt": "check auth"})
	m.handleEvent(claude.AssistantMessage{
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub1", Name: "Task", Input: taskIn}},
		Usage:  &claude.Usage{InputTokens: 4000, OutputTokens: 120, CacheReadInputTokens: 2000},
	})
	grepIn, _ := json.Marshal(map[string]any{"pattern": "oauth"})
	m.handleEvent(claude.AssistantMessage{
		Parent: "sub1",
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub1_g1", Name: "Grep", Input: grepIn}},
		Usage:  &claude.Usage{InputTokens: 2800, OutputTokens: 300},
	})
	m.refresh()
	view := ansi.Strip(m.View())
	for _, want := range []string{"AGENTS", "1 active", "Inspect auth flow", "Grep oauth", "◇ 1 agent"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in active subagent view:\n%s", want, view)
		}
	}

	// Completing the Task marks it done and preserves its token/tool summary.
	res, _ := json.Marshal("Found OAuth handler in auth.go:42")
	m.handleEvent(claude.ToolResults{
		Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "sub1", Content: res}},
	})
	m.refresh()
	doneView := ansi.Strip(m.View())
	if !strings.Contains(doneView, "1 done") || !strings.Contains(doneView, "◇ Task") {
		t.Fatalf("expected completed subagent badge and ◇ Task card:\n%s", doneView)
	}
}

func TestPerTurnReceiptAndTouchedFiles(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.engine, m.model = "claude", "claude-haiku-4-5-20251001"
	m.Update(tea.WindowSizeMsg{Width: 132, Height: 38})
	m.busy = true

	editIn, _ := json.Marshal(map[string]any{
		"file_path":  "/tmp/cart.go",
		"old_string": "return sum * discount",
		"new_string": "return sum * (1 - discount)",
	})
	m.handleEvent(claude.AssistantMessage{
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "e1", Name: "Edit", Input: editIn}},
	})
	okRes, _ := json.Marshal("updated")
	m.handleEvent(claude.ToolResults{
		Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "e1", Content: okRes}},
	})
	m.handleEvent(claude.Result{
		Subtype:    "success",
		NumTurns:   1,
		DurationMS: 4200,
		CostUSD:    0.015,
		Usage:      claude.Usage{InputTokens: 4000, CacheReadInputTokens: 14900, OutputTokens: 312},
	})
	m.refresh()
	view := ansi.Strip(m.View())
	for _, want := range []string{"FILES", "cart.go", "+1 -1", "Worked for 4.2s", "18.9k in", "312 out", "78% cached", "$0.015", "1 tool"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in turn receipt / rail view:\n%s", want, view)
		}
	}
}

func TestCaptureTelemetryPreview(t *testing.T) {
	out := os.Getenv("TEVEUS_TELEMETRY_CAP")
	if out == "" {
		t.Skip("set TEVEUS_TELEMETRY_CAP to capture preview frames")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme(themes[0])

	m := New(Config{
		Claude:   claude.Options{Cwd: "/Users/stawan15/teveus/docs/demo-app", Model: "haiku"},
		Dark:     true,
		Settings: Settings{Onboarded: true, Engine: "claude", ClaudeNotice: true, Concise: true, LeanTools: true, Effort: "high"},
		Version:  "0.36.1",
	})
	m.Update(tea.WindowSizeMsg{Width: 128, Height: 38})
	m.engine, m.model, m.branch, m.sessionID = "claude", "claude-haiku-4-5-20251001", "main", "89f25fbc"
	m.fiveHour = &claude.Window{Utilization: 0.14, ResetsAt: time.Now().Add(4 * time.Hour).Unix()}
	m.sevenDay = &claude.Window{Utilization: 0.12, ResetsAt: time.Now().Add(4 * 24 * time.Hour).Unix()}
	m.monthly = &claude.Window{Utilization: 0.62, ResetsAt: time.Now().Add(12 * 24 * time.Hour).Unix()}

	// Turn 1: completed with Thai prompt, subagent, edit, and per-turn token receipt.
	m.add(&block{kind: kindUser, text: "ช่วยเช็คบั๊กคำนวณส่วนลดใน cart.go แล้วแก้ให้หน่อย ⚠ อย่าลืมตรวจสอบเทสด้วย"})
	m.busy = true
	subIn, _ := json.Marshal(map[string]any{"description": "Audit cart tests", "prompt": "check tests"})
	m.handleEvent(claude.AssistantMessage{
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub0", Name: "Task", Input: subIn}},
	})
	grepIn, _ := json.Marshal(map[string]any{"pattern": "Total"})
	m.handleEvent(claude.AssistantMessage{
		Parent: "sub0",
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub0_g", Name: "Grep", Input: grepIn}},
		Usage:  &claude.Usage{InputTokens: 1100, OutputTokens: 180},
	})
	okG, _ := json.Marshal("cart_test.go:7: Total([]float64{10, 20}, 0.1)")
	m.handleEvent(claude.ToolResults{Parent: "sub0", Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "sub0_g", Content: okG}}})
	okSub, _ := json.Marshal("Found failing assertion in cart_test.go:7")
	m.handleEvent(claude.ToolResults{Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "sub0", Content: okSub}}})

	editIn, _ := json.Marshal(map[string]any{
		"file_path":  "/Users/stawan15/teveus/docs/demo-app/cart.go",
		"old_string": "\treturn sum * discount",
		"new_string": "\treturn sum * (1 - discount)",
	})
	m.handleEvent(claude.AssistantMessage{
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "e1", Name: "Edit", Input: editIn}},
	})
	okE, _ := json.Marshal("updated")
	m.handleEvent(claude.ToolResults{Results: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "e1", Content: okE}}})
	m.add(&block{kind: kindAssistant, text: "แก้บั๊กบรรทัดที่ 9 ใน `cart.go` แล้วครับ เปลี่ยนจาก `sum * discount` เป็น `sum * (1 - discount)` เพื่อให้หักส่วนลด 10% จริงๆ"})
	m.handleEvent(claude.Result{
		Subtype: "success", NumTurns: 1, DurationMS: 4200, CostUSD: 0.015,
		Usage: claude.Usage{InputTokens: 4000, CacheReadInputTokens: 14900, OutputTokens: 312},
	})
	m.context = 18900

	// Turn 2: live active subagent running right now!
	m.add(&block{kind: kindUser, text: "ลองสำรวจ edge case อื่นๆ ในโปรเจกต์ด้วย subagent"})
	m.busy, m.turnStart, m.phase = true, time.Now().Add(-3200*time.Millisecond), "Running Task"
	sub2In, _ := json.Marshal(map[string]any{"description": "Inspect edge cases", "prompt": "check edge cases"})
	m.handleEvent(claude.AssistantMessage{
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub2", Name: "Task", Input: sub2In}},
		Usage:  &claude.Usage{InputTokens: 3200, CacheReadInputTokens: 16200, OutputTokens: 140},
	})
	g2In, _ := json.Marshal(map[string]any{"pattern": "discount"})
	m.handleEvent(claude.AssistantMessage{
		Parent: "sub2",
		Blocks: []claude.ContentBlock{{Type: "tool_use", ID: "sub2_g", Name: "Grep", Input: g2In}},
		Usage:  &claude.Usage{InputTokens: 2900, OutputTokens: 210},
	})
	m.refresh()

	type frame struct {
		T    int64  `json:"t"`
		S    string `json:"s"`
		Mark string `json:"mark,omitempty"`
	}
	os.MkdirAll(out, 0o755)
	b, _ := json.Marshal([]frame{{T: 0, S: m.View(), Mark: "atelier-telemetry"}})
	if err := os.WriteFile(filepath.Join(out, "frames.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
