package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// Reasoning effort: how hard the model thinks before answering. "auto"
// leaves it to the model (or, on Claude Code, to its own /config).
var effortLevels = []struct{ name, desc string }{
	{"auto", "the model's own default"},
	{"low", "quickest and cheapest · questions, small edits, simple lookups"},
	{"medium", "balanced · everyday coding"},
	{"high", "thinks it through · tricky bugs, design decisions"},
	{"xhigh", "deep reasoning · the sweet spot for coding on the newest models"},
	{"max", "as much thinking as it takes · the hardest problems, costs the most"},
}

func (m *Model) effort() string {
	if m.settings.Effort == "" {
		return "auto"
	}
	return m.settings.Effort
}

func (m *Model) openEffortSlider() {
	var cs []choice
	sel := 0
	for i, l := range effortLevels {
		name := l.name
		if name == m.effort() {
			sel = i
		}
		cs = append(cs, choice{label: name, value: name, desc: l.desc, run: func(m *Model) tea.Cmd { return m.setEffort(name) }})
	}
	m.openSlider("Reasoning effort", "How hard the model thinks before it answers", cs, sel)
	m.pop.ends = [2]string{"faster · cheaper", "smarter · slower"}
	m.pop.colors = neutralFirst(len(cs))
}

// setEffort applies a level: from the next request on the Direct API engine,
// by restarting Claude Code (keeping the conversation) on the Claude engine.
func (m *Model) setEffort(level string) tea.Cmd {
	if level == "" {
		m.openEffortSlider()
		return nil
	}
	valid := false
	for _, l := range effortLevels {
		valid = valid || l.name == level
	}
	if !valid {
		m.note("effort levels: auto, low, medium, high, xhigh, max", false)
		return nil
	}
	if level == m.effort() {
		return nil
	}
	m.settings.Effort = level
	if level == "auto" {
		m.settings.Effort = ""
	}
	saveSettings(m.settings)
	if eng, ok := m.client.(*agent.Engine); ok {
		eng.SetEffort(m.settings.Effort)
		m.note("effort → "+level, true)
		return nil
	}
	return m.restart("effort → " + level)
}
