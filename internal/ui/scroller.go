package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// scroller is the transcript's viewport. Unlike bubbles' viewport it doesn't
// measure every line when the content changes, only the visible ones when
// drawing, so long sessions stay fast: the content is replaced on every
// streamed chunk.
type scroller struct {
	Width, Height   int
	YOffset         int
	MouseWheelDelta int
	lines           []string
}

func (s *scroller) SetLines(lines []string) {
	s.lines = lines
	if s.YOffset > s.maxYOffset() {
		s.GotoBottom()
	}
}

func (s *scroller) SetContent(c string) { s.SetLines(strings.Split(c, "\n")) }

func (s *scroller) TotalLineCount() int { return len(s.lines) }

func (s *scroller) maxYOffset() int { return max(0, len(s.lines)-s.Height) }

func (s *scroller) AtBottom() bool { return s.YOffset >= s.maxYOffset() }

func (s *scroller) setYOffset(y int) { s.YOffset = max(0, min(y, s.maxYOffset())) }

func (s *scroller) GotoTop()         { s.YOffset = 0 }
func (s *scroller) GotoBottom()      { s.YOffset = s.maxYOffset() }
func (s *scroller) ScrollDown(n int) { s.setYOffset(s.YOffset + n) }
func (s *scroller) ScrollUp(n int)   { s.setYOffset(s.YOffset - n) }
func (s *scroller) HalfViewDown()    { s.ScrollDown(s.Height / 2) }
func (s *scroller) HalfViewUp()      { s.ScrollUp(s.Height / 2) }

// Update handles the mouse wheel.
func (s scroller) Update(msg tea.Msg) (scroller, tea.Cmd) {
	if m, ok := msg.(tea.MouseMsg); ok && m.Action == tea.MouseActionPress {
		switch m.Button {
		case tea.MouseButtonWheelUp:
			s.ScrollUp(s.MouseWheelDelta)
		case tea.MouseButtonWheelDown:
			s.ScrollDown(s.MouseWheelDelta)
		}
	}
	return s, nil
}

// visible returns the lines in view.
func (s *scroller) visible() []string {
	top := min(s.YOffset, len(s.lines))
	return s.lines[top:min(top+s.Height, len(s.lines))]
}

func (s *scroller) View() string {
	return lipgloss.NewStyle().Width(s.Width).Height(s.Height).MaxHeight(s.Height).MaxWidth(s.Width).
		Render(strings.Join(s.visible(), "\n"))
}
