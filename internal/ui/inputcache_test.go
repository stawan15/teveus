package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A cursor move that changes what the textarea draws must redraw the box. An
// arrow along a line changes neither the value nor the cursor's line, and the
// box's cache used to miss that, so the text stayed drawn from before the move.
func TestInputBoxFollowsCursor(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.input.SetValue(strings.Repeat("word ", 40)) // long enough to scroll
	_ = m.View()

	before := m.inputView()
	for i := 0; i < 40; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		if m.inputView() != before {
			return
		}
	}
	t.Fatal("the box never redrew while the cursor scrolled the text")
}

// The cache still does its job: a redraw with nothing moved reuses the box.
func TestInputBoxCacheStillHits(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.input.SetValue("hello")
	first := m.inputView()
	if second := m.inputView(); second != first || m.inputKey == "" {
		t.Fatalf("the box was rebuilt with nothing changed")
	}
}
