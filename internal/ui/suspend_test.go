package ui

import (
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
	_, cmd := m.Update(tea.ResumeMsg{})
	if cmd == nil {
		t.Fatal("resuming didn't ask for a repaint")
	}
	if _, ok := cmd().(repaintMsg); !ok {
		t.Fatalf("resume sent %T, want repaintMsg", cmd())
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "still here after fg") {
		t.Fatalf("the conversation isn't redrawn after resuming:\n%s", view)
	}
}
