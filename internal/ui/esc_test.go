package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

type fakeBackend struct {
	claude.Backend
	interrupts int
}

func (f *fakeBackend) Interrupt() error { f.interrupts++; return nil }

func TestEscTwiceInterrupts(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	fb := &fakeBackend{}
	m.client, m.busy = fb, true
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fb.interrupts != 0 || m.notice != "press esc again to interrupt" {
		t.Fatalf("first esc: interrupts=%d notice=%q", fb.interrupts, m.notice)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if fb.interrupts != 1 {
		t.Fatalf("second esc: interrupts=%d", fb.interrupts)
	}
}
