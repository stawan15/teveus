package ui

import (
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// Conversations on the Direct API engine are saved for /resume. They are
// deleted after keepSessionsDays (30 by default, -1 keeps them), and /purge
// deletes them and the prompt history at once. Claude Code keeps its own
// sessions in ~/.claude; teveus leaves those alone.

const defaultKeepDays = 30

func (m *Model) keepDays() int {
	if m.settings.KeepSessionsDays == 0 {
		return defaultKeepDays
	}
	return m.settings.KeepSessionsDays
}

// pruneSessions runs once at startup.
func (m *Model) pruneSessions() tea.Cmd {
	days := m.keepDays()
	if days < 0 {
		return nil
	}
	return func() tea.Msg {
		agent.PruneSessions(sessionDir(), time.Duration(days)*24*time.Hour)
		return nil
	}
}

func (m *Model) openPurge() {
	m.openPicker("Delete teveus's saved data?", []choice{
		{label: "Delete conversations and prompt history", desc: "saved Direct API conversations (/resume) and ↑ history · keys and settings stay",
			run: func(m *Model) tea.Cmd {
				err1 := os.RemoveAll(sessionDir())
				err2 := os.Remove(filepath.Join(configDir(), "history.json"))
				m.history, m.histIdx = nil, -1
				if err1 != nil || (err2 != nil && !os.IsNotExist(err2)) {
					m.note("couldn't delete everything; see "+configDir(), false)
				} else {
					m.note("✓ deleted saved conversations and prompt history", true)
				}
				return nil
			}},
		{label: "Cancel", run: func(*Model) tea.Cmd { return nil }},
	}, 1)
}
