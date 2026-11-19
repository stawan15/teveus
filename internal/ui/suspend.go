package ui

import tea "github.com/charmbracelet/bubbletea"

// Suspender is the part of *tea.Program SuspendGuard drives.
type Suspender interface {
	ReleaseTerminal() error
	RestoreTerminal() error
	Send(tea.Msg)
}
