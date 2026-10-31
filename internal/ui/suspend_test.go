package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCtrlZSuspendsThroughBubbleTea(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// ctrl+z must ask Bubble Tea to suspend, because only it releases the
	// alternate screen and the key protocol before the process stops. Letting
	// the terminal do it leaves (fg) drawing into an unrestored screen.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if cmd == nil {
		t.Fatal("ctrl+z did nothing")
	}
	if _, ok := cmd().(tea.SuspendMsg); !ok {
		t.Fatalf("ctrl+z sent %T, want tea.SuspendMsg", cmd())
	}
}

func TestResumeRedraws(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.add(&block{kind: kindUser, text: "still here after fg"})

	// Resuming asks for a clear, so the shell's leftovers are erased and the
	// renderer, which still thinks its last frame is on screen, draws again.
	// Mouse capture goes back on too: releasing the terminal turned it off and
	// restoring doesn't turn it back on, so the wheel would stay dead.
	_, cmd := m.Update(tea.ResumeMsg{})
	if cmd == nil {
		t.Fatal("resuming didn't ask for a repaint")
	}
	var repaint, mouse bool
	for _, c := range cmdsOf(cmd) {
		msg := c()
		if _, ok := msg.(repaintMsg); ok {
			repaint = true
		}
		// The mouse commands return an unexported message; the type name is
		// the only handle on it from outside the package.
		if msg != nil && strings.Contains(fmt.Sprintf("%T", msg), "enableMouseCellMotion") {
			mouse = true
		}
	}
	if !repaint {
		t.Fatal("resuming didn't ask for a repaint")
	}
	if !mouse {
		t.Fatal("resuming didn't turn mouse capture back on")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "still here after fg") {
		t.Fatalf("the conversation isn't redrawn after resuming:\n%s", view)
	}
}

// cmdsOf flattens a command into the commands it runs. Batch runs its parts
// without ordering, so a test has to look at each of them.
func cmdsOf(c tea.Cmd) []tea.Cmd {
	if c == nil {
		return nil
	}
	if b, ok := c().(tea.BatchMsg); ok {
		return b
	}
	return []tea.Cmd{c}
}
