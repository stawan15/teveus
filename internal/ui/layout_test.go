package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Every line of the input box, including the top border that carries the
// mode label, must be exactly as wide as the screen.
func TestInputBoxEdgesAlign(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	for _, w := range []int{60, 100, 137} {
		for _, mode := range []string{"default", "acceptEdits", "plan", "auto"} {
			m := New(Config{Dark: true})
			m.mode = mode
			m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
			for i, l := range strings.Split(m.inputView(), "\n") {
				if got := ansi.StringWidth(l); got != w {
					t.Fatalf("width %d mode %s line %d is %d wide: %q", w, mode, i, got, ansi.Strip(l))
				}
			}
			for _, view := range []string{m.View()} {
				for i, l := range strings.Split(view, "\n") {
					if ansi.StringWidth(l) > w {
						t.Fatalf("width %d: view line %d overflows (%d)", w, i, ansi.StringWidth(l))
					}
				}
			}
		}
	}
}
