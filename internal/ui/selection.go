package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The app captures the mouse (for wheel scrolling and clickable tool cards),
// which takes text selection away from the terminal, so it selects itself:
// drag to select (auto-scrolling past the edges), double-click for a word,
// triple-click for a line. Releasing copies, like most terminals.

// clipboardWrite is swapped out in tests so they don't touch the real clipboard.
var clipboardWrite = copyToClipboard

type point struct{ line, col int }

func (a point) before(b point) bool { return a.line < b.line || (a.line == b.line && a.col < b.col) }

type selection struct {
	dragging  bool
	has       bool // something is selected
	anchor    point
	head      point
	row       int // mouse row relative to the viewport, for auto-scroll
	x         int
	lastPress time.Time
	lastPoint point
	clicks    int
}

func (s *selection) bounds() (point, point) {
	if s.head.before(s.anchor) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// span returns the selected columns [from, to) of a content line.
func (s *selection) span(line, width int) (int, int, bool) {
	if !s.has {
		return 0, 0, false
	}
	a, b := s.bounds()
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	from, to := 0, width
	if line == a.line {
		from = a.col
	}
	if line == b.line {
		to = b.col + 1
	}
	from, to = max(0, min(from, width)), max(0, min(to, width))
	return from, to, to > from
}

func (m *Model) pointAt(x, row int) point {
	row = max(0, min(row, m.vp.Height-1))
	line := min(m.vp.YOffset+row, max(len(m.lines)-1, 0))
	return point{line: line, col: max(0, min(x, m.chatW-1))}
}

func (m *Model) plainLine(i int) string {
	if i < 0 || i >= len(m.lines) {
		return ""
	}
	return ansi.Strip(m.lines[i])
}

// handleMouse: wheel scrolls; left button selects; a click without a drag
// toggles the tool card under it.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	row := msg.Y - 1 // header line
	switch {
	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		if m.sel.dragging {
			m.sel.head = m.pointAt(m.sel.x, m.sel.row)
		}
		return cmd

	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if row < 0 || row >= m.vp.Height || msg.X >= m.chatW {
			m.clearSelection()
			return nil
		}
		p := m.pointAt(msg.X, row)
		if time.Since(m.sel.lastPress) < 400*time.Millisecond && p == m.sel.lastPoint {
			m.sel.clicks++
		} else {
			m.sel.clicks = 1
		}
		m.sel.lastPress, m.sel.lastPoint = time.Now(), p
		m.sel.dragging, m.sel.has, m.sel.anchor, m.sel.head, m.sel.row, m.sel.x = true, false, p, p, row, msg.X
		switch m.sel.clicks {
		case 2:
			m.selectWord(p)
		case 3:
			m.selectLine(p)
		}
		return nil

	// A release the terminal never reported (it went to the window edge or to
	// the terminal's own link handling) would leave the drag stuck, and
	// autoScroll would keep scrolling the transcript up.
	case msg.Action == tea.MouseActionMotion && m.sel.dragging && msg.Button != tea.MouseButtonLeft:
		m.sel.dragging = false
		return nil

	case msg.Action == tea.MouseActionMotion && m.sel.dragging:
		m.sel.row, m.sel.x = row, msg.X
		m.sel.head = m.pointAt(msg.X, row)
		if m.sel.clicks == 1 && m.sel.head != m.sel.anchor {
			m.sel.has = true
		}
		return nil

	case msg.Action == tea.MouseActionRelease && m.sel.dragging:
		m.sel.dragging = false
		if m.sel.has {
			m.copySelection()
			return nil
		}
		m.toggleToolAt(m.sel.anchor.line)
		return nil
	}
	return nil
}

// autoScroll runs on every tick while dragging past the viewport edge.
func (m *Model) autoScroll() {
	if !m.sel.dragging {
		return
	}
	switch {
	case m.sel.row < 0:
		m.vp.ScrollUp(max(1, -m.sel.row))
	case m.sel.row >= m.vp.Height:
		m.vp.ScrollDown(max(1, m.sel.row-m.vp.Height+1))
	default:
		return
	}
	m.sel.head = m.pointAt(m.sel.x, m.sel.row)
	m.sel.has = m.sel.head != m.sel.anchor
}

func (m *Model) toggleToolAt(line int) {
	for i := len(m.blockStart) - 1; i >= 0; i-- {
		if m.blockStart[i] <= line {
			if b := m.blocks[i]; b.kind == kindTool {
				b.open = !b.open
				b.invalidate()
				m.refresh()
			}
			return
		}
	}
}

func (m *Model) selectWord(p point) {
	runes := []rune(m.plainLine(p.line))
	// columns and runes line up for the common single-width case
	if p.col >= len(runes) || unicode.IsSpace(runes[p.col]) {
		return
	}
	word := func(r rune) bool { return !unicode.IsSpace(r) && !strings.ContainsRune("│┃⎿()[]{}\"'`,;", r) }
	from, to := p.col, p.col
	for from > 0 && word(runes[from-1]) {
		from--
	}
	for to < len(runes)-1 && word(runes[to+1]) {
		to++
	}
	m.sel.anchor, m.sel.head, m.sel.has = point{p.line, from}, point{p.line, to}, true
}

func (m *Model) selectLine(p point) {
	line := m.plainLine(p.line)
	trimmed := strings.TrimRight(line, " ")
	start := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if trimmed == "" {
		return
	}
	m.sel.anchor, m.sel.head, m.sel.has = point{p.line, start}, point{p.line, ansi.StringWidth(trimmed) - 1}, true
}

func (m *Model) clearSelection() {
	m.sel.has, m.sel.dragging = false, false
}

// selectedText returns the selection as plain text, trailing spaces and the
// common indentation removed so pasted code keeps its shape.
func (m *Model) selectedText() string {
	a, b := m.sel.bounds()
	var lines []string
	for i := a.line; i <= b.line && i < len(m.lines); i++ {
		plain := m.plainLine(i)
		from, to, ok := m.sel.span(i, ansi.StringWidth(plain))
		if !ok {
			lines = append(lines, "")
			continue
		}
		if i == a.line && strings.TrimSpace(ansi.Cut(plain, 0, from)) == "" {
			from = 0 // starting at the first word: keep its indentation for dedenting
		}
		lines = append(lines, strings.TrimRight(ansi.Cut(plain, from, to), " "))
	}
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range lines {
		if indent > 0 && len(l) >= indent {
			lines[i] = l[indent:]
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func (m *Model) copySelection() {
	text := m.selectedText()
	if text == "" {
		m.clearSelection()
		return
	}
	clipboardWrite(text)
	n := len([]rune(text))
	lines := strings.Count(text, "\n") + 1
	msg := fmt.Sprintf("copied %d characters", n)
	if lines > 1 {
		msg = fmt.Sprintf("copied %d lines", lines)
	}
	m.note(msg, true)
}

// chatView renders the visible transcript with the selection highlighted.
func (m *Model) chatView() string {
	if !m.sel.has {
		return m.vp.View()
	}
	hl := lipgloss.NewStyle().Background(cAccent2).Foreground(cOnAccent)
	start := min(m.vp.YOffset, len(m.lines))
	end := min(start+m.vp.Height, len(m.lines))
	vis := make([]string, 0, m.vp.Height)
	for i := start; i < end; i++ {
		l := m.lines[i]
		w := ansi.StringWidth(l)
		if from, to, ok := m.sel.span(i, w); ok {
			l = ansi.Cut(l, 0, from) + hl.Render(ansi.Strip(ansi.Cut(l, from, to))) + ansi.Cut(l, to, w)
		}
		vis = append(vis, l)
	}
	return lipgloss.NewStyle().
		Width(m.vp.Width).Height(m.vp.Height).
		MaxWidth(m.vp.Width).MaxHeight(m.vp.Height).
		Render(strings.Join(vis, "\n"))
}
