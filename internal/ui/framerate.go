package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The renderer redraws at a capped frame rate, and every changed buffer is a
// full repaint of the window sent to the terminal. 60 is what Bubble Tea uses
// by default and what every terminal keeps up with; 120 halves the delay
// between a keystroke and its echo, at the cost of twice the repaints while a
// reply streams. A terminal that can't keep up (over SSH, in tmux, a large
// window) shows that as lag, so the rate is the user's to pick.

const defaultFrameRate = 120

// FrameRate is the rate to start the renderer at: the setting, or the default
// when it is unset. The program is built once, so this is read at startup.
func FrameRate(s Settings) int {
	if s.FrameRate > 0 {
		return s.FrameRate
	}
	return defaultFrameRate
}

var frameRates = []int{30, 60, 120}

var frameRateDescs = map[int]string{
	30:  "slowest terminal (SSH, tmux, a big window): fewest repaints",
	60:  "the Bubble Tea default; every terminal keeps up",
	120: "fastest echo; twice the repaints while a reply streams",
}

// frameRate is the rate the program was started with (0 means the default).
func (m *Model) frameRate() int { return FrameRate(m.settings) }

// openFrameRateSlider shows the rates to pick from.
func (m *Model) openFrameRateSlider() {
	var cs []choice
	sel := 0
	for i, r := range frameRates {
		r := r
		if r == m.frameRate() {
			sel = i
		}
		cs = append(cs, choice{label: fmt.Sprintf("%d fps", r), value: fmt.Sprint(r), desc: frameRateDescs[r],
			run: func(m *Model) tea.Cmd { return m.setFrameRate(fmt.Sprint(r)) }})
	}
	m.openSlider("Frame rate", "How often the window redraws · lower it if scrolling or typing feels laggy", cs, sel)
	m.pop.colors = neutralFirst(len(cs))
}

// setFrameRate changes the rate; without one it opens the slider. The
// renderer is built when the program starts, so a change needs a restart,
// which is what the note says.
func (m *Model) setFrameRate(v string) tea.Cmd {
	v = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(v), "fps"))
	if v == "" {
		m.openFrameRateSlider()
		return nil
	}
	want := 0
	for _, r := range frameRates {
		if fmt.Sprint(r) == v {
			want = r
		}
	}
	if want == 0 {
		m.note("frame rates: 30, 60, 120", false)
		return nil
	}
	if want == m.frameRate() {
		return nil
	}
	m.settings.FrameRate = want
	saveSettings(m.settings)
	m.note(fmt.Sprintf("frame rate %d → restart teveus to apply", want), true)
	return nil
}
