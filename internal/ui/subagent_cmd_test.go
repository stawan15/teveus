package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func TestSubagentModelSetting(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.engine = "api"
	m.models = []claude.ModelInfo{{Value: "openai/gpt-mini", DisplayName: "gpt-mini"}, {Value: "openai/gpt-big", DisplayName: "gpt-big"}}

	if m.settings.SubagentModel != "" {
		t.Fatal("must be off until the user turns it on")
	}
	m.openSubagentPicker("")
	if !m.pop.open() || len(m.pop.all) != 3 || m.pop.all[0].label != "Same as the main model" {
		t.Fatalf("picker: %+v", m.pop.all)
	}
	m.openSubagentPicker("openai/gpt-mini")
	if m.settings.SubagentModel != "openai/gpt-mini" || LoadSettings().SubagentModel != "openai/gpt-mini" {
		t.Fatal("the choice was not saved")
	}
	m.openSubagentPicker("off")
	if m.settings.SubagentModel != "" || LoadSettings().SubagentModel != "" {
		t.Fatal("off did not clear it")
	}

	m.engine = "claude"
	m.pop.close()
	m.openSubagentPicker("")
	if m.pop.open() {
		t.Fatal("Claude Code has no such setting")
	}
}
