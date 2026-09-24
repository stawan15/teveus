package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// setSubagentModel picks the model the research subagents run on, in every
// open session. "" means the same one as the conversation. Off by default:
// a smaller model is cheaper but may search less well.
func (m *Model) setSubagentModel(model string) {
	m.settings.SubagentModel = model
	saveSettings(m.settings)
	m.save()
	for _, s := range m.sessions {
		if eng, ok := s.client.(*agent.Engine); ok {
			eng.SetSubagentModel(model)
		}
	}
	if model == "" {
		m.note("research subagents use the main model", true)
	} else {
		m.note("research subagents → "+model, true)
	}
}

// openSubagentPicker offers the connected models, or "arg" directly.
func (m *Model) openSubagentPicker(arg string) tea.Cmd {
	if m.engine != "api" {
		m.note("subagents only exist on the Direct API engine (Claude Code manages its own)", false)
		return nil
	}
	switch arg {
	case "":
	case "off", "main", "same":
		m.setSubagentModel("")
		return nil
	default:
		m.setSubagentModel(arg)
		return nil
	}
	cs := []choice{{label: "Same as the main model", desc: "the default", run: func(m *Model) tea.Cmd { m.setSubagentModel(""); return nil }}}
	sel := 0
	for _, mi := range m.models {
		v := mi.Value
		if v == m.settings.SubagentModel {
			sel = len(cs)
		}
		c := choice{label: mi.DisplayName, value: v, desc: mi.Description, key: mi.Meta, group: mi.Group,
			run: func(m *Model) tea.Cmd { m.setSubagentModel(v); return nil }}
		if c.label == "" {
			c.label = v
		}
		if mi.Free {
			c.badge = "free"
		}
		cs = append(cs, c)
	}
	m.openPicker("Model for research subagents · cheaper, but may search less well", cs, sel)
	return nil
}
