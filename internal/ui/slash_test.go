package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func slashPopup(t *testing.T, input string) []choice {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.commands = []claude.Command{{Name: "review", Description: "Review a pull request"}}
	m.input.SetValue(input)
	m.afterInput()
	return m.pop.list
}

func names(cs []choice) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		out[c.label] = true
	}
	return out
}

func TestSlashListsOnlyCoreCommandsUntilYouType(t *testing.T) {
	got := names(slashPopup(t, "/"))
	if len(got) != len(coreCmds) {
		t.Fatalf("got %d commands, want %d: %v", len(got), len(coreCmds), got)
	}
	for c := range coreCmds {
		if !got["/"+c] {
			t.Errorf("/%s missing", c)
		}
	}
}

func TestSlashSearchFindsHiddenCommands(t *testing.T) {
	for in, want := range map[string]string{"/pur": "/purge", "/mou": "/mouse", "/rev": "/review"} {
		if !names(slashPopup(t, in))[want] {
			t.Errorf("%s did not find %s", in, want)
		}
	}
}
