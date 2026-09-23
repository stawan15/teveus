package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string {
	lines := strings.Split(ansiRE.ReplaceAllString(s, ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

// Drives the real model against a live claude process. Opt-in:
// TEVEUS_SMOKE=1 TEVEUS_DIR=/some/dir go test ./internal/ui -run Smoke -v
func TestSmoke(t *testing.T) {
	if os.Getenv("TEVEUS_SMOKE") == "" {
		t.Skip("set TEVEUS_SMOKE=1")
	}
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	dir := os.Getenv("TEVEUS_DIR")
	os.WriteFile(dir+"/notes.md", []byte("# notes\n"), 0o644)

	m := New(Config{Claude: claude.Options{Cwd: dir, Model: "haiku"}, Dark: true})
	msgs := make(chan tea.Msg, 1024)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, c := range b {
					run(c)
				}
				return
			}
			if msg != nil {
				msgs <- msg
			}
		}()
	}
	step := func(msg tea.Msg) { _, c := m.Update(msg); run(c) }
	typ := func(s string) {
		for _, r := range s {
			if r == ' ' {
				step(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			} else {
				step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}
	}
	key := func(k tea.KeyType) { step(tea.KeyMsg{Type: k}) }
	pump := func(d time.Duration, until func() bool) {
		deadline := time.After(d)
		for !until() {
			select {
			case msg := <-msgs:
				step(msg)
			case <-deadline:
				t.Log("timeout")
				return
			}
		}
	}
	snap := func(name string) { t.Log(name + ":\n" + plain(m.View())) }

	run(m.Init())
	step(tea.WindowSizeMsg{Width: 120, Height: 34})
	pump(15*time.Second, func() bool { return len(m.commands) > 0 && m.files != nil })
	snap("welcome")

	step(tea.KeyMsg{Type: tea.KeyCtrlK})
	snap("palette")
	typ("them")
	key(tea.KeyEnter)
	key(tea.KeyDown)
	snap("theme picker previewing " + theme.Name)
	key(tea.KeyEsc)
	t.Logf("theme after esc: %s", theme.Name)

	typ("/eff")
	snap("slash popup")
	key(tea.KeyEnter)
	snap("effort picker")
	key(tea.KeyEsc)

	typ("look at @not")
	snap("mention")
	key(tea.KeyTab)
	t.Logf("input after mention: %q", m.input.Value())
	step(tea.KeyMsg{Type: tea.KeyCtrlC})

	typ("Create hello.txt containing hi with the Write tool, then reply done.")
	key(tea.KeyEnter)
	pump(90*time.Second, func() bool {
		if len(m.perms) > 0 {
			snap("permission")
			typ("y")
		}
		return !m.busy
	})
	snap("after turn")

	typ("/context")
	key(tea.KeyEsc) // close popup, keep text
	key(tea.KeyEnter)
	pump(30*time.Second, func() bool { return !m.busy })
	key(tea.KeyUp)
	t.Logf("history up: %q", m.input.Value())
	key(tea.KeyUp)
	t.Logf("history up 2: %q", m.input.Value())
	snap("final")
	m.client.Close()
}
