package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/stawan15/teveus/internal/claude"
)

// TestCaptureDemo records the README demo without calling an AI provider.
// It drives the real UI with a scripted turn so docs can be regenerated
// deterministically; docs/screencast renders the frames into the GIF and
// screenshots.
//
//	TEVEUS_CAPTURE=out/dir TEVEUS_CAPTURE_PROJECT=~/demo-app go test ./internal/ui -run CaptureDemo
func TestCaptureDemo(t *testing.T) {
	out, project := os.Getenv("TEVEUS_CAPTURE"), os.Getenv("TEVEUS_CAPTURE_PROJECT")
	if out == "" || project == "" {
		t.Skip("set TEVEUS_CAPTURE and TEVEUS_CAPTURE_PROJECT")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)

	type frame struct {
		T    int64  `json:"t"`
		S    string `json:"s"`
		Mark string `json:"mark,omitempty"`
	}
	var frames []frame
	var at int64
	record := func(m *Model, mark string) {
		m.refresh()
		frames = append(frames, frame{T: at, S: m.View(), Mark: mark})
	}
	wait := func(ms int64) { at += ms }

	m := New(Config{
		Claude:   claude.Options{Cwd: project, Model: "haiku"},
		Dark:     true,
		Settings: Settings{Onboarded: true, Engine: "claude", ClaudeNotice: true, Level: "standard"},
		Version:  "0.10.0",
	})
	m.Update(tea.WindowSizeMsg{Width: 118, Height: 34})
	m.engine, m.model = "claude", "claude-haiku-4-5-20251001"
	m.branch = gitBranch(project)
	m.sessionID = "89f25fbc"
	record(m, "welcome")
	wait(1800)

	const prompt = "The test fails. Fix the bug in cart.go (don't run anything)."
	for _, r := range prompt {
		if r == ' ' {
			m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		} else {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		record(m, "")
		wait(35)
	}
	wait(500)

	m.input.Reset()
	m.turnStart = time.Now()
	m.busy, m.phase = true, "Thinking"
	m.add(&block{kind: kindUser, text: prompt})
	for i := 0; i < 4; i++ {
		m.r.spin = spinFrames[i]
		record(m, "")
		wait(160)
	}

	bash := &block{kind: kindTool, id: "t1", name: "Bash", state: toolDone,
		input:  map[string]any{"command": "go test ./..."},
		result: "--- FAIL: TestTotal (0.00s)\n    cart_test.go:7: Total = 3, want 27\nFAIL", started: time.Now().Add(-1500 * time.Millisecond), finished: time.Now()}
	m.add(bash)
	m.phase = "Reading the test"
	record(m, "")
	wait(800)

	testFile := "package demo\n\nimport \"testing\"\n\nfunc TestTotal(t *testing.T) {\n\tif got := Total([]float64{10, 20}, 0.1); got != 27 {\n\t\tt.Fatalf(\"Total = %v, want 27\", got)\n\t}\n}\n"
	m.add(&block{kind: kindTool, id: "t2", name: "Read", state: toolDone,
		input: map[string]any{"file_path": filepath.Join(project, "cart_test.go")}, result: testFile,
		started: time.Now().Add(-900 * time.Millisecond), finished: time.Now()})
	m.phase = "Reading cart.go"
	record(m, "")
	wait(700)

	cartFile := "package demo\n\n// Total returns the price of all items after the discount (0.1 = 10% off).\nfunc Total(prices []float64, discount float64) float64 {\n\tsum := 0.0\n\tfor _, p := range prices {\n\t\tsum += p\n\t}\n\treturn sum * discount\n}\n"
	m.add(&block{kind: kindTool, id: "t3", name: "Read", state: toolDone,
		input: map[string]any{"file_path": filepath.Join(project, "cart.go")}, result: cartFile,
		started: time.Now().Add(-500 * time.Millisecond), finished: time.Now()})
	m.phase = "Writing"
	record(m, "")
	wait(600)

	stream := &block{kind: kindAssistant, streaming: true}
	m.add(stream)
	for _, chunk := range []string{
		"The bug is on line 9. ",
		"`Total` multiplies the sum by the discount, ",
		"so a 10% discount charges 10% instead of taking 10% off. ",
		"With 30 in items it should be 27, not 3.",
	} {
		stream.text += chunk
		stream.invalidate()
		record(m, "")
		wait(420)
	}
	stream.streaming = false
	stream.invalidate()
	record(m, "")
	wait(500)

	m.context = 18900
	m.fiveHour = &claude.Window{Utilization: 0.16, ResetsAt: time.Now().Add(5 * time.Hour).Unix()}
	m.sevenDay = &claude.Window{Utilization: 0.02, ResetsAt: time.Now().Add(7 * 24 * time.Hour).Unix()}

	edit := &block{kind: kindTool, id: "t4", name: "Edit", state: toolRunning,
		input: map[string]any{
			"file_path":  filepath.Join(project, "cart.go"),
			"old_string": "\treturn sum * discount",
			"new_string": "\treturn sum * (1 - discount)",
		},
		started: time.Now().Add(-400 * time.Millisecond)}
	m.add(edit)
	m.phase = "Waiting for your approval"
	m.layout()
	record(m, "")
	wait(500)

	approval, _ := json.Marshal(map[string]any{
		"file_path":  filepath.Join(project, "cart.go"),
		"old_string": "\treturn sum * discount",
		"new_string": "\treturn sum * (1 - discount)",
	})
	m.handleEvent(&claude.PermissionRequest{
		RequestID: "p1", ToolName: "Edit", ToolUseID: "t4", Description: "cart.go", Input: approval,
		Suggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Edit"}],"behavior":"allow","destination":"session"}]`),
	})
	m.layout()
	record(m, "approval")
	wait(1800)

	m.perms = nil
	edit.state, edit.finished = toolDone, time.Now()
	edit.result = "The file cart.go has been updated."
	edit.invalidate()
	m.phase = "Finishing"
	record(m, "")
	wait(900)

	m.busy, m.phase = false, ""
	m.cost = 0.015
	m.add(&block{kind: kindTurnEnd, text: "Worked for 4.2s · $0.015"})
	m.refresh()
	m.vp.GotoBottom()
	record(m, "done")
	wait(1800)

	m.openPalette()
	record(m, "palette")
	wait(1600)

	m.pop.close()
	m.openThemePicker()
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	record(m, "theme")
	wait(1800)
	applyTheme(themes[0])

	os.MkdirAll(out, 0o755)
	b, _ := json.Marshal(frames)
	if err := os.WriteFile(filepath.Join(out, "frames.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d frames over %dms", len(frames), at)
}
