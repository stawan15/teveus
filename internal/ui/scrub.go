package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// scrubSessions masks the secrets in every conversation teveus has saved.
// New ones are redacted as they are written (agent.SaveSession); this is for
// the files that were written before that, and for a key that was pasted into
// a conversation that is still on disk.
//
// It rewrites files in place, so it reports what it touched rather than doing
// it silently.
func (m *Model) scrubSessions() tea.Cmd {
	// The live engine holds the conversation in memory too; teach the redactor
	// the keys this install has before the next save picks them up.
	agent.LoadKnownSecrets(configDir())
	n, err := agent.ScrubSessions(sessionDir())
	if err != nil {
		m.add(&block{kind: kindError, text: "couldn't clean the saved conversations: " + err.Error()})
		m.refresh()
		return nil
	}
	var msg string
	if n == 0 {
		msg = "**Masked saved conversations**\n\nNo secrets found in the conversations on disk."
	} else {
		msg = fmt.Sprintf("**Masked saved conversations**\n\nCleaned %s. Keys now read `[redacted:…4]` in the files, and new conversations are masked as they are saved.",
			plural(n, "%d saved conversation"))
	}
	m.add(&block{kind: kindCmdOut, text: msg})
	m.refresh()
	return nil
}
