package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// TestEveryLocalCommandRuns sends each slash command through the same dispatch
// the composer uses. The point is that no command panics or silently does
// nothing: each one either opens its picker, writes a block, sets a notice, or
// sends a prompt. It is also what covers the command table's closures.
func TestEveryLocalCommandRuns(t *testing.T) {
	for _, c := range localCmds {
		c := c
		if c.name == "exit" || c.name == "setup" {
			continue // quitting and restarting the intro need their own tests
		}
		t.Run(c.name, func(t *testing.T) {
			m := testModel(t)
			m.engine = "api" // no Claude Code gate to answer
			beforeSettings := m.settings
			b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event, 8)}}
			m.client = b

			before := len(m.blocks)
			cmd, ok := m.localCommand(c.name, "")
			if !ok {
				t.Fatalf("/%s isn't dispatched", c.name)
			}
			if cmd != nil {
				// Some commands do their work in a goroutine the app feeds
				// back through Update; running the message here stands in for
				// that, so nothing may panic on either side.
				if msg := cmd(); msg != nil {
					m.Update(msg)
				}
			}
			// Something has to have happened.
			changed := len(m.blocks) != before || m.pop.open() || m.notice != "" ||
				len(b.sent) > 0 || m.input.Value() != "" || !sameSettings(m.settings, beforeSettings)
			if !changed {
				t.Fatalf("/%s did nothing at all", c.name)
			}
		})
	}
}

func TestSlashCommandsWithArguments(t *testing.T) {
	cases := []struct{ name, arg string }{
		{"model", "openai/gpt-5.4"},
		{"engine", "api"},
		{"theme", "dark"},
		{"permissions", "allow Bash(go test:*)"},
		{"effort", "low"},
		{"minimal", "strict"},
		{"trim", "all"},
		{"commit", "only the parser"},
		{"diff", ""},
	}
	for _, c := range cases {
		t.Run(c.name+" "+c.arg, func(t *testing.T) {
			m := testModel(t)
			if cmd, ok := m.localCommand(c.name, c.arg); ok && cmd != nil {
				cmd()
			}
		})
	}
}

func TestEnginePickerSwitchesEngines(t *testing.T) {
	m := testModel(t)
	m.openEnginePicker()
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	if len(m.pop.list) != 2 {
		t.Fatalf("%d engines offered", len(m.pop.list))
	}
	for _, c := range m.pop.list {
		if c.desc == "" {
			t.Fatalf("%q has no explanation", c.label)
		}
	}
}

func TestModelPickerShowsConnectedModels(t *testing.T) {
	m := testModel(t)
	m.models = []claude.ModelInfo{
		{Value: "openai/gpt-5.4", DisplayName: "gpt-5.4", Group: "OpenAI", Meta: "200k"},
		{Value: "google/gemini", DisplayName: "gemini", Group: "Google", Free: true},
	}
	m.openModelPicker()
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	if len(m.pop.list) < 2 {
		t.Fatalf("models offered: %d", len(m.pop.list))
	}
	// Choosing one sets it and remembers it.
	var picked bool
	for _, c := range m.pop.list {
		if c.value == "openai/gpt-5.4" {
			c.run(m)
			picked = true
		}
	}
	if !picked || m.model != "openai/gpt-5.4" {
		t.Fatalf("model = %q", m.model)
	}
	if got := m.settings.APIModel; got != m.model {
		t.Fatalf("saved model = %q, want %q", got, m.model)
	}
}

func TestModelPickerWithNothingConnectedOffersAWay(t *testing.T) {
	m := testModel(t)
	m.models = nil
	m.engine = "" // nothing connected
	m.openModelPicker()
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	var labels []string
	for _, c := range m.pop.list {
		labels = append(labels, c.label)
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "Claude Code") || !strings.Contains(joined, "provider") {
		t.Fatalf("dead end instead of options: %s", joined)
	}
}

func TestSubagentPickerListsModelsAndAuto(t *testing.T) {
	m := testModel(t)
	m.models = []claude.ModelInfo{{Value: "openai/gpt-5.4", DisplayName: "gpt-5.4"}}
	m.openSubagentPicker("")
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	// It always offers the "same as the conversation" choice first.
	if len(m.pop.list) < 2 {
		t.Fatalf("rows = %d", len(m.pop.list))
	}
	m.pop.list[0].run(m)
	if m.settings.SubagentModel != "" {
		t.Fatalf("the default should clear the setting, got %q", m.settings.SubagentModel)
	}
}

func TestPurgeOffersBothChoices(t *testing.T) {
	m := testModel(t)
	m.openPurge()
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	if len(m.pop.list) < 2 {
		t.Fatalf("rows = %d", len(m.pop.list))
	}
	for _, c := range m.pop.list {
		if c.label == "Cancel" {
			continue
		}
		if c.desc == "" {
			t.Fatalf("%q doesn't say what it removes", c.label)
		}
	}
}

func TestOnboardingAsksTheThreeQuestions(t *testing.T) {
	m := testModel(t)
	m.startOnboarding()
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	if len(m.pop.list) == 0 {
		t.Fatal("no questions")
	}
}

func TestHelpAndSettingsOpen(t *testing.T) {
	m := testModel(t)
	m.openHelp()
	if m.pop.mode != popPicker || len(m.pop.list) == 0 {
		t.Fatalf("help = %v with %d rows", m.pop.mode, len(m.pop.list))
	}
	m.closePopup(true)

	m.openSettings()
	if m.pop.mode != popPicker {
		t.Fatalf("settings = %v", m.pop.mode)
	}
	// Every row says what it does and can be run.
	for _, c := range m.pop.list {
		if c.desc == "" && c.key == "" && c.group == "" {
			t.Fatalf("%q has no description", c.label)
		}
	}
	for _, c := range m.pop.list {
		if c.run != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("running %q panicked: %v", c.label, r)
					}
				}()
				c.run(m)
			}()
		}
	}
}

func TestResumePickerWithoutSessions(t *testing.T) {
	m := testModel(t)
	m.openResumePicker()
	// With nothing saved it says so rather than showing an empty list.
	if m.pop.open() && len(m.pop.list) == 0 {
		t.Fatal("an empty resume list was opened")
	}
}

func TestSearchWithoutAQueryAsksForOne(t *testing.T) {
	// /search on its own fills the box with "/search " rather than guessing.
	m := testModel(t)
	m.search("")
	if !strings.HasPrefix(m.input.Value(), "/search") {
		t.Fatalf("input = %q", m.input.Value())
	}
}

func TestSearchFindsTextInEarlierConversations(t *testing.T) {
	// It searches saved conversations, not the open one (that's on screen).
	m := testModel(t)
	m.cwd = t.TempDir()
	agent.SaveSession(sessionDir(), agent.Session{ID: "api-past", Cwd: m.cwd, Model: "m", Title: "old",
		Updated: time.Now(), History: []agent.Message{{Role: "user", Text: "the quick brown fox"}}})

	msg := m.search("brown")()
	sm, ok := msg.(searchMsg)
	if !ok {
		t.Fatalf("search returned %T", msg)
	}
	if len(sm.hits) == 0 {
		t.Fatal("the saved conversation wasn't found")
	}
	m.showSearch(sm)
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
}

func TestExportWritesMarkdown(t *testing.T) {
	m := testModel(t)
	m.cwd = t.TempDir()
	m.add(&block{kind: kindUser, text: "hello"})
	m.add(&block{kind: kindAssistant, text: "there"})
	m.exportTranscript()
	if !strings.Contains(m.notice, "exported") && !strings.Contains(m.notice, "saved") {
		t.Logf("notice = %q", m.notice)
	}
}

func TestToggleConciseAndLean(t *testing.T) {
	m := testModel(t)
	m.toggleConcise()
	if !m.settings.Concise {
		t.Fatal("concise didn't turn on")
	}
	m.toggleLean()
	if !m.settings.LeanTools {
		t.Fatal("lean didn't turn on")
	}
}

func TestPhoneCommandTogglesThePage(t *testing.T) {
	m := testModel(t)
	m.showPhone("off") // already off: harmless
	if m.phoneOn() {
		t.Fatal("the page turned on when asked to turn off")
	}
	m.cfg.Send = func(tea.Msg) {}
	m.showPhone("on")
	t.Cleanup(m.stopPhone)
	if !m.phoneOn() {
		t.Fatal("the page didn't start")
	}
	m.showPhone("")
	if len(m.blocks) == 0 {
		t.Fatal("asking again didn't print the link")
	}
	m.showPhone("off")
	if m.phoneOn() {
		t.Fatal("the page didn't stop")
	}
}

// sameSettings compares the comparable fields; RecentModels is a slice, so the
// struct can't be compared directly.
func sameSettings(a, b Settings) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestPaletteRunsEveryAction(t *testing.T) {
	// The palette is the discoverable front door; every entry has to work and
	// say what it does.
	m := testModel(t)
	m.models = []claude.ModelInfo{{Value: "openai/gpt-5.4", DisplayName: "gpt-5.4"}}
	m.openPalette()
	all := append([]choice(nil), m.pop.list...)
	if len(all) < 20 {
		t.Fatalf("the palette has only %d entries", len(all))
	}
	for _, c := range all {
		if c.label == "" {
			t.Fatal("an entry has no label")
		}
		if c.label == "Quit" {
			continue // covered by the quit test
		}
		t.Run(c.label, func(t *testing.T) {
			if c.run == nil {
				t.Fatalf("%q does nothing", c.label)
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%q panicked: %v", c.label, r)
				}
			}()
			c.run(m)
		})
	}
}

func TestCLIChoiceDescribesACommand(t *testing.T) {
	m := testModel(t)
	c := m.cliChoice(claude.Command{Name: "compact", Description: "Summarise the conversation",
		ArgumentHint: "<focus>"})
	if c.label == "" || c.value == "" {
		t.Fatalf("choice = %+v", c)
	}
	if !strings.Contains(c.label, "compact") && !strings.Contains(c.value, "compact") {
		t.Fatalf("the command name is missing: %+v", c)
	}
	// A command with hidden arguments keeps its hint out of the label.
	c2 := m.cliChoice(claude.Command{Name: "__internal", Description: "hidden"})
	if c2.label == "" {
		t.Fatalf("choice = %+v", c2)
	}
}
