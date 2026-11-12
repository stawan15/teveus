package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The input box is drawn from a cache, so it must notice every change that
// shows: text, cursor line, height, width and the mode pill.
func TestInputBoxFollowsItsContents(t *testing.T) {
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	before := m.inputView()

	m.input.SetValue("hello")
	if got := m.inputView(); got == before || !strings.Contains(got, "hello") {
		t.Fatal("typed text did not reach the box")
	}
	typed := m.inputView()

	m.input.SetValue("hello\nworld")
	m.afterInput() // sizes the box to the lines it holds
	if got := m.inputView(); got == typed {
		t.Fatal("a second line did not change the box")
	}
	two := m.inputView()

	m.setMode("acceptEdits")
	if got := m.inputView(); got == two {
		t.Fatal("the mode label did not change the box")
	}
	mode := m.inputView()

	m.w = 100
	if got := m.inputView(); got == mode {
		t.Fatal("a resize did not change the box")
	}
}
