package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/stawan15/teveus/internal/claude"
)

// TestCapture records a real teveus session for the README demo. It drives
// the live Claude Code engine (haiku) on a small project and saves every
// screen change with its time; docs/screencast renders them into a GIF.
//
//	TEVEUS_CAPTURE=out/dir TEVEUS_CAPTURE_PROJECT=~/demo-app go test ./internal/ui -run Capture -timeout 5m
func TestCapture(t *testing.T) {
	out, project := os.Getenv("TEVEUS_CAPTURE"), os.Getenv("TEVEUS_CAPTURE_PROJECT")
	if out == "" || project == "" {
		t.Skip("set TEVEUS_CAPTURE and TEVEUS_CAPTURE_PROJECT")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)

	type frame struct {
		T    int64  `json:"t"` // ms since start
		S    string `json:"s"`
		Mark string `json:"mark,omitempty"`
	}
	var frames []frame
	start := time.Now()
	m := New(Config{Claude: claude.Options{Cwd: project, Model: "haiku"}, Dark: true, Settings: LoadSettings(), Version: "0.1.1"})
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
	record := func(mark string) {
		v := m.View()
		if n := len(frames); n > 0 && frames[n-1].S == v && mark == "" {
			return
		}
		frames = append(frames, frame{T: time.Since(start).Milliseconds(), S: v, Mark: mark})
	}
	step := func(msg tea.Msg) {
		_, c := m.Update(msg)
		run(c)
		record("")
	}
	// pump processes messages for up to d, or until cond is true.
	pump := func(d time.Duration, cond func() bool) bool {
		deadline := time.After(d)
		for cond == nil || !cond() {
			select {
			case msg := <-msgs:
				step(msg)
			case <-deadline:
				return false
			}
		}
		return true
	}
	hold := func(d time.Duration) { pump(d, nil) }
	typ := func(s string) {
		for _, r := range s {
			if r == ' ' {
				step(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			} else {
				step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			hold(38 * time.Millisecond)
		}
	}
	press := func(k tea.KeyType) { step(tea.KeyMsg{Type: k}) }

	run(m.Init())
	step(tea.WindowSizeMsg{Width: 118, Height: 34})
	pump(20*time.Second, func() bool { return len(m.commands) > 0 })
	hold(1500 * time.Millisecond)
	record("welcome")
	hold(1200 * time.Millisecond)

	typ("The test fails. Fix the bug in cart.go (don't run anything).")
	hold(500 * time.Millisecond)
	press(tea.KeyEnter)
	if !pump(60*time.Second, func() bool { return len(m.perms) > 0 }) {
		t.Fatal("no approval prompt")
	}
	hold(1800 * time.Millisecond)
	record("approval")
	hold(900 * time.Millisecond)
	typ("y")
	if !pump(90*time.Second, func() bool { return !m.busy }) {
		t.Fatal("turn did not finish")
	}
	hold(1500 * time.Millisecond)
	record("done")
	hold(1500 * time.Millisecond)

	step(tea.KeyMsg{Type: tea.KeyCtrlK})
	hold(1300 * time.Millisecond)
	record("palette")
	typ("theme")
	hold(500 * time.Millisecond)
	press(tea.KeyEnter)
	hold(900 * time.Millisecond)
	for i := 0; i < 3; i++ {
		press(tea.KeyDown)
		hold(1000 * time.Millisecond)
		if i == 1 {
			record("theme")
		}
	}
	press(tea.KeyEsc)
	hold(1200 * time.Millisecond)
	m.client.Close()

	os.MkdirAll(out, 0o755)
	b, _ := json.Marshal(frames)
	if err := os.WriteFile(filepath.Join(out, "frames.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d frames over %s", len(frames), time.Since(start).Round(time.Second))
}
