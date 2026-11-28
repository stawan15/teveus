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

// blockAt returns the transcript block that owns line i, or nil.
func (m *Model) blockAt(line int) *block {
	for i := len(m.blockStart) - 1; i >= 0; i-- {
		if m.blockStart[i] <= line {
			return m.blocks[i]
		}
	}
	return nil
}

// gutterWidth returns the display column right after a leading UI border
// gutter (such as " ┃ " on user messages, "  │ " on tool output, or "  │" on
// command output and blockquotes). When allowTable is false, lines with
// another "│" later on the row are treated as Markdown tables and left alone.
func gutterWidth(plain string, allowTable bool) int {
	col := 0
	i := 0
	for i < len(plain) && plain[i] == ' ' {
		i++
		col++
	}
	// UI gutters sit near the left edge (1-cell margin + up to 6-cell nested
	// tool indent + 2-cell prefix).
	if col > 10 || i >= len(plain) {
		return 0
	}
	rest := plain[i:]
	var barLen int
	switch {
	case strings.HasPrefix(rest, "│"):
		barLen = len("│")
	case strings.HasPrefix(rest, "┃"):
		barLen = len("┃")
	case strings.HasPrefix(rest, "▎"):
		barLen = len("▎")
	case strings.HasPrefix(rest, "▏"):
		barLen = len("▏")
	default:
		return 0
	}
	after := rest[barLen:]
	if !allowTable && strings.ContainsRune(after, '│') {
		return 0
	}
	col++ // box-drawing bar is 1 cell wide
	if strings.HasPrefix(after, " ") {
		col++
	}
	return col
}

// gutterCol returns the column where selectable content begins on line i,
// skipping any leading UI gutter border.
func (m *Model) gutterCol(line int) int {
	allowTable := false
	if b := m.blockAt(line); b != nil && (b.kind == kindUser || b.kind == kindTool || b.kind == kindCmdOut) {
		allowTable = true
	}
	return gutterWidth(m.plainLine(line), allowTable)
}

// handleMouse: wheel scrolls; left button selects; a click without a drag
// toggles the tool card under it.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	row := msg.Y - 1 // header line
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && len(m.queue) > 0 && !m.pop.open() && len(m.perms) == 0 {
		lines := strings.Count(m.bottomFitted(), "\n") + 1
		qLines := strings.Count(m.queueView(), "\n") + 1
		if i := msg.Y - (m.h - 1 - lines); i >= 0 && i < qLines {
			if i == 2 && len(m.queue) > 3 {
				i = len(m.queue) - 1 // the "N more" line
			}
			m.editQueued(i)
			return nil
		}
	}
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
	if trimmed == "" {
		return
	}
	g := m.gutterCol(p.line)
	end := ansi.StringWidth(trimmed)
	if g >= end {
		return
	}
	content := ansi.Cut(trimmed, g, end)
	start := g + (len(content) - len(strings.TrimLeft(content, " ")))
	if start >= end {
		return
	}
	m.sel.anchor, m.sel.head, m.sel.has = point{p.line, start}, point{p.line, end - 1}, true
}

func (m *Model) clearSelection() {
	m.sel.has, m.sel.dragging = false, false
}

// softWrapped reports whether line i only ends because the renderer ran out
// of width: the line is padded to the wrap width and the next word would not
// have fit on it.
func (m *Model) softWrapped(i int) bool {
	cur := m.plainLine(i)
	nextRaw := m.plainLine(i + 1)
	next := strings.TrimSpace(ansi.Cut(nextRaw, m.gutterCol(i+1), ansi.StringWidth(nextRaw)))
	if next == "" || strings.HasPrefix(next, "•") || strings.HasPrefix(next, "│") {
		return false
	}
	w := ansi.StringWidth(cur)
	if w < min(m.chatW-3, 120)-3 {
		return false // not padded to the wrap width, so it ended on its own
	}
	word, _, _ := strings.Cut(next, " ")
	return ansi.StringWidth(strings.TrimRight(cur, " "))+1+ansi.StringWidth(word) > w-4
}

// hyphenBreak reports whether the renderer broke a word after a hyphen (no
// space was lost) rather than at a space before a minus sign.
func hyphenBreak(line, next string) bool {
	if !strings.HasSuffix(line, "-") {
		return false
	}
	before := strings.TrimSuffix(strings.TrimSuffix(line, "-"), "-")
	next = strings.TrimLeft(next, " ")
	return !strings.HasSuffix(before, " ") || next != "" && unicode.IsLetter([]rune(next)[0])
}

// tabIndented reports whether the block holding transcript line i has code
// indented with tabs. The renderer shows each tab as four spaces.
func (m *Model) tabIndented(line int) bool {
	for i := len(m.blockStart) - 1; i >= 0; i-- {
		if m.blockStart[i] <= line {
			b := m.blocks[i]
			return b.kind == kindAssistant && strings.Contains(b.text, "\n\t")
		}
	}
	return false
}

// spacesToTabs turns each leading group of four spaces back into a tab.
func spacesToTabs(l string) string {
	rest := strings.TrimLeft(l, " ")
	n := len(l) - len(rest)
	return strings.Repeat("\t", n/4) + strings.Repeat(" ", n%4) + rest
}

// selectedText returns the selection as plain text, trailing spaces and the
// common indentation removed so pasted code keeps its shape. Lines that were
// only wrapped for the screen are joined back together.
func (m *Model) selectedText() string {
	a, b := m.sel.bounds()
	var lines []string
	var src []int // the transcript line each entry came from
	for i := a.line; i <= b.line && i < len(m.lines); i++ {
		plain := m.plainLine(i)
		from, to, ok := m.sel.span(i, ansi.StringWidth(plain))
		if !ok {
			lines, src = append(lines, ""), append(src, i)
			continue
		}
		g := m.gutterCol(i)
		if from < g {
			from = g
		}
		if to <= from {
			lines, src = append(lines, ""), append(src, i)
			continue
		}
		if i == a.line && strings.TrimSpace(ansi.Cut(plain, g, from)) == "" {
			from = g // starting at the first word: keep its indentation for dedenting
		}
		piece := strings.TrimRight(ansi.Cut(plain, from, to), " ")
		if k := len(lines) - 1; i > a.line && k >= 0 && lines[k] != "" && piece != "" && m.softWrapped(i-1) {
			if !hyphenBreak(lines[k], piece) {
				lines[k] += " "
			}
			lines[k] += strings.TrimLeft(piece, " ")
			continue
		}
		lines, src = append(lines, piece), append(src, i)
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
		if m.tabIndented(src[i]) {
			lines[i] = spacesToTabs(lines[i])
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
	return strings.Join(m.chatLines(), "\n")
}

// chatLines returns the visible transcript rows directly without joining them,
// so View() can assemble the frame without an extra Join+Split cycle.
func (m *Model) chatLines() []string {
	if !m.sel.has {
		return m.vp.ViewLines()
	}
	hl := lipgloss.NewStyle().Background(cAccent2).Foreground(cOnAccent)
	width, height := max(m.vp.Width, 0), max(m.vp.Height, 0)
	blank := strings.Repeat(" ", width)
	start := min(m.vp.YOffset, len(m.lines))
	end := min(start+height, len(m.lines))
	vis := make([]string, 0, height)
	for i := start; i < end; i++ {
		l := m.lines[i]
		w := ansi.StringWidth(l)
		if from, to, ok := m.sel.span(i, w); ok {
			if g := m.gutterCol(i); g > from {
				from = g
			}
			if to > from {
				l = ansi.Cut(l, 0, from) + hl.Render(ansi.Strip(ansi.Cut(l, from, to))) + ansi.Cut(l, to, w)
			}
		}
		switch lw := lineWidth(l); {
		case lw > width:
			l = ansi.Truncate(l, width, "")
		case lw < width:
			l += blank[lw:]
		}
		vis = append(vis, l)
	}
	for len(vis) < height {
		vis = append(vis, blank)
	}
	return vis
}
