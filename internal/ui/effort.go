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

// effortInUse is the level the current model is actually asked for, and
// whether the user's pick had to be lowered to get there. Claude Code takes
// the CLI's own wording, so its scale is trusted as-is.
func (m *Model) effortInUse() (string, bool) {
	want := m.settings.Effort
	if want == "" {
		return "auto", false
	}
	if m.engine != "api" {
		return want, false
	}
	return agent.ClampToModel(want, m.model, m.modelReasoning(m.model))
}

// modelReasoning is the levels the provider published for a model, if it did.
func (m *Model) modelReasoning(value string) []string {
	for _, mi := range m.models {
		if mi.Value == value {
			return mi.Reasoning
		}
	}
	return nil
}

func (m *Model) openEffortSlider() {
	var cs []choice
	sel := 0
	// Levels above what this model takes can't be reached, so they say so
	// instead of pretending a setting will be sent.
	cap, capped := m.effortCeiling()
	for i, l := range effortLevels {
		name, desc := l.name, l.desc
		if name == m.effort() {
			sel = i
		}
		if capped && effortRankOf(name) > effortRankOf(cap) {
			desc = "this model stops at " + cap
		}
		cs = append(cs, choice{label: name, value: name, desc: desc, run: func(m *Model) tea.Cmd { return m.setEffort(name) }})
	}
	m.openSlider("Reasoning effort", "How hard the model thinks before it answers", cs, sel)
	m.pop.ends = [2]string{"faster · cheaper", "smarter · slower"}
	m.pop.colors = neutralFirst(len(cs))
}

// effortCeiling is the highest level the current model takes, and whether that
// is below the top of the scale.
func (m *Model) effortCeiling() (string, bool) {
	if m.engine != "api" {
		return "max", false
	}
	lvl, capped := agent.ClampToModel("max", m.model, m.modelReasoning(m.model))
	return lvl, capped
}

func effortRankOf(level string) int {
	for i, l := range effortLevels {
		if l.name == level {
			return i
		}
	}
	return 0
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
