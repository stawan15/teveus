package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

const sidebarWidth = 32

// paneMargin is the horizontal breathing gutter inside the right editorial
// canvas: 2 cells on desktop so prose, cards, composer and status bar share
// one crisp vertical alignment line away from the Left Rail divider.
func (m *Model) paneMargin() int {
	if m.sideW > 0 && m.paneW() >= 48 {
		return 2
	}
	return 1
}

// paneW is the width of the right editorial canvas (full width when the left
// architectural rail is hidden or on narrow terminals).
func (m *Model) paneW() int {
	return max(m.w-m.sideW, 1)
}

// topHeight is the chrome above the transcript viewport: the header row,
// its hairline divider, and the optional multi-session tab strip.
func (m *Model) topHeight() int { return 2 + m.tabsHeight() }

// layout sizes the viewport around the header, the bottom area (popup,
// input or permission prompt) and the status bar.
func (m *Model) layout() {
	if m.w == 0 {
		return
	}
	m.layoutWidths()
	m.layoutViewport(lipgloss.Height(m.bottomFitted()))
}

func (m *Model) layoutWidths() {
	m.sideW = 0
	if !m.settings.HideSide && m.w >= 112 {
		m.sideW = sidebarWidth
	}
	m.chatW = m.w - m.sideW
	inPad := 4
	if m.sideW > 0 {
		inPad = 4 + m.paneMargin()*2
	}
	wantW := max(m.paneW()-inPad, 10)
	if m.input.Width() != wantW {
		m.input.SetWidth(wantW)
	}
}

func (m *Model) layoutViewport(bottomH int) {
	vpH := m.h - m.topHeight() - 1 - bottomH
	m.vp.Width, m.vp.Height = m.chatW, max(vpH, 1)
}

// refresh re-renders the transcript into the viewport, following the
// bottom unless the user has scrolled up. Every block but the last is kept
// from the previous refresh, so a streamed chunk draws one block instead of
// rebuilding the whole session.
func (m *Model) refresh() {
	if m.w == 0 {
		return
	}
	follow := m.vp.AtBottom() || m.vp.TotalLineCount() <= m.vp.Height
	gutter := strings.Repeat(" ", m.paneMargin())
	width := min(m.chatW-m.paneMargin()*2-1, 108) // calm editorial measure with breathing room
	if width < 16 {
		gutter = " "
		width = max(m.chatW-3, 8)
	}
	// The last block is left out of the cache because streaming mutates it.
	// When the tail is a folded read-tool chain, keep the whole tail run out
	// of the prefix cache so a newly completed Read merges cleanly with it.
	cut := max(len(m.blocks)-1, 0)
	if !m.r.expand && len(m.blocks) > 0 && isReadTool(m.blocks[len(m.blocks)-1]) {
		for cut > 0 && isReadTool(m.blocks[cut-1]) {
			cut--
		}
	}
	// Keep the prefix only while the blocks in it are still the ones it was
	// drawn from and still say the same thing. A cache with no blocks holds
	// only the head (the welcome text), which the first block replaces.
	c := m.tc
	keep := c.width == width && c.level == m.level() && c.expand == m.r.expand &&
		c.theme == theme.Name && len(c.blocks) <= cut &&
		(len(c.blocks) > 0 || len(m.blocks) == 0)
	for i := 0; keep && i < len(c.blocks); i++ {
		keep = c.blocks[i] == m.blocks[i] && !c.blocks[i].needsPaint(width)
	}
	var lines []string
	var starts []int
	base, rest := 0, ""
	if keep {
		base, rest = len(c.blocks), c.rest
		lines, starts = m.lines[:c.nLines], m.blockStart[:c.nStarts]
	} else {
		lines, starts = m.lines[:0], m.blockStart[:0]
		head := "\n"
		if len(m.blocks) == 0 {
			head = m.welcome(width)
		}
		lines, rest = appendText(lines, "", head, gutter)
	}
	m.warm(m.blocks[base:cut], width)
	saved := false
	var next prefixCache
	for i := base; i < len(m.blocks); i++ {
		if i == cut {
			saved = true
			next = prefixCache{nLines: len(lines), nStarts: len(starts), rest: rest,
				width: width, level: m.level(), expand: m.r.expand, theme: theme.Name}
		}
		if i > 0 {
			// Proximity: consecutive tool calls sit together, turns breathe.
			// Pro is compact: blank lines only between turns.
			sep := "\n\n"
			switch {
			case m.blocks[i].kind == kindUser:
				// Generous breathing room opens a new human turn without extra clutter.
				sep = "\n\n\n"
			case (m.blocks[i-1].kind == kindTool && m.blocks[i].kind == kindTool) || m.level() == "pro":
				sep = "\n"
			}
			lines, rest = appendText(lines, rest, sep, gutter)
		}
		if !m.r.expand && isReadTool(m.blocks[i]) {
			j := i + 1
			for j < len(m.blocks) && isReadTool(m.blocks[j]) {
				j++
			}
			if j-i >= 2 {
				rowStart := len(lines)
				for k := i; k < j; k++ {
					starts = append(starts, rowStart)
				}
				lines, rest = appendText(lines, rest, m.r.renderReadChain(m.blocks[i:j], width), gutter)
				i = j - 1
				continue
			}
		}
		starts = append(starts, len(lines))
		lines, rest = appendText(lines, rest, m.r.render(m.blocks[i], width), gutter)
	}
	if !saved {
		next = prefixCache{nLines: len(lines), nStarts: len(starts), rest: rest,
			width: width, level: m.level(), expand: m.r.expand, theme: theme.Name}
	}
	next.blocks = append(m.tc.blocks[:base], m.blocks[base:cut]...)
	// The unterminated line and the blank line the transcript ends with are
	// only for display: the cache keeps the text they came from, so the next
	// block continues that line instead of starting a second copy of it.
	lines, _ = appendText(lines, rest, "\n", gutter)
	m.lines, m.blockStart, m.tc = lines, starts, next
	m.vp.SetLines(m.lines)
	if follow {
		m.vp.GotoBottom()
	}
	if m.phone != nil {
		m.phone.snapshot(m)
	}
}

// warm renders blocks that still need paint before the loop that joins them,
// spreading the work over the cores when there is enough of it. glamour keeps
// state inside a renderer, so each worker draws with its own; the blocks it
// fills are only read afterwards, by this goroutine.
//
// A resumed conversation is the case this is for: opening 3,000 saved blocks
// meant 3,000 markdown renders in a row, and most of the second it took.
func (m *Model) warm(blocks []*block, width int) {
	var todo []*block
	for _, b := range blocks {
		if b.needsPaint(width) {
			todo = append(todo, b)
		}
	}
	if len(todo) < 24 || runtime.GOMAXPROCS(0) < 2 {
		return // not worth the goroutines; render() will do these in the loop
	}
	workers := min(runtime.GOMAXPROCS(0), len(todo))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := renderer{cwd: m.r.cwd, expand: m.r.expand, spin: m.r.spin}
			for {
				i := int(next.Add(1) - 1)
				if i >= len(todo) {
					return
				}
				r.render(todo[i], width)
			}
		}()
	}
	wg.Wait()
}

// prefixCache is the transcript drawn up to the last block: how many lines it
// took, the blocks they came from, and the line still unterminated.
type prefixCache struct {
	blocks  []*block
	nLines  int
	nStarts int
	rest    string
	width   int
	level   string
	expand  bool
	theme   string
}

// appendText splits text into transcript lines, continuing any unterminated
// line it started, and returns the unterminated remainder.
func appendText(lines []string, rest, text, gutter string) ([]string, string) {
	parts := strings.Split(rest+text, "\n")
	// Left margin added by hand: lipgloss would measure and pad every line of
	// the whole transcript on each streamed chunk. Tabs are replaced because
	// tab stops skew widths, and SARA AM is decomposed here before fitting.
	for _, p := range parts[:len(parts)-1] {
		lines = append(lines, gutter+strings.ReplaceAll(termSafe(p), "\t", "    "))
	}
	return lines, termSafe(parts[len(parts)-1])
}

func (m *Model) View() string {
	if m.w == 0 {
		return ""
	}
	// Size the transcript to what the bottom area needs right now, so a
	// popup opened from anywhere can never push the frame past the window.
	// Compute bottomFitted once and reuse it instead of rendering it twice.
	m.layoutWidths()
	bottom := m.bottomFitted()
	m.layoutViewport(lipgloss.Height(bottom))
	leftRows := make([]string, 0, m.h)
	if t := m.tabStrip(); t != "" {
		leftRows = append(leftRows, strings.Split(t, "\n")...)
	}
	leftRows = append(leftRows, m.chatLines()...)
	leftRows = append(leftRows, strings.Split(bottom, "\n")...)
	leftRows = append(leftRows, m.statusBar())
	bodyLines := leftRows
	if m.sideW > 0 {
		// Right Architectural Rail: the transcript stays on the left, and
		// joinSideLines pads/truncates every left row to exact m.chatW before
		// appending the right rail, so │ stays laser-straight on column m.chatW.
		bodyH := max(m.h-2, 1)
		bodyLines = joinSideLines(leftRows, m.sidebarLines(bodyH), m.chatW)
	}
	rows := make([]string, 0, m.h+2)
	rows = append(rows, m.header(), m.headerRule())
	rows = append(rows, bodyLines...)
	return m.clampLines(rows)
}

// joinSides puts the left pane and the right sidebar on the same rows, left to
// right. Every left row is fitted to exact leftW cells so the right sidebar's
// │ border stays on column leftW even when a block or prose line is shorter.
func joinSides(left, right string, leftW int) string {
	return strings.Join(joinSideLines(strings.Split(left, "\n"), strings.Split(right, "\n"), leftW), "\n")
}

// joinSideLines puts the transcript and the sidebar on the same rows, left to
// right, without joining and re-splitting strings.
func joinSideLines(l, r []string, leftW int) []string {
	out := make([]string, max(len(l), len(r)))
	pad := strings.Repeat(" ", max(leftW, 0))
	for i := range out {
		var a, b string
		if i < len(l) {
			a = l[i]
		}
		if w := lineWidth(a); w < leftW {
			a += pad[:leftW-w]
		} else if w > leftW {
			a = ansi.Truncate(a, leftW, "")
		}
		if i < len(r) {
			b = r[i]
		}
		out[i] = a + b
	}
	return out
}

// clampLines fits each row directly without a whole-frame Join+Split cycle.
func (m *Model) clampLines(lines []string) string {
	n := len(lines)
	if n > m.h {
		n = m.h
	}
	if cap(m.clamped) < n {
		m.clamped = make([]string, 0, n+8)
	}
	m.clamped = m.clamped[:n]
	for i := 0; i < n; i++ {
		m.clamped[i] = m.fitRow(termSafe(lines[i]))
	}
	return strings.Join(m.clamped, "\n")
}

// fitRow pads or cuts one frame row to the window width. Most rows are the
// same string from frame to frame (the sidebar, the header, the status bar)
// or differ in a way that leaves the width alone, and measuring a styled row
// means walking its graphemes — 21% of a frame before this cache.
func (m *Model) fitRow(l string) string {
	target := m.w // the width the row is fitted to
	if m.rowCache == nil {
		m.rowCache = make(map[string]string, 64)
	}
	if target != m.rowCacheW {
		for k := range m.rowCache {
			delete(m.rowCache, k)
		}
		m.rowCacheW, m.rowPad = target, strings.Repeat(" ", target)
	}
	if out, ok := m.rowCache[l]; ok {
		return out
	}
	out := l
	switch w := lineWidth(l); {
	case w > target:
		out = ansi.Truncate(l, target, "")
	case w < target:
		out = l + m.rowPad[w:]
	}
	if len(m.rowCache) >= 1024 {
		clear(m.rowCache)
	}
	m.rowCache[l] = out
	return out
}

// bottomFitted is the bottom area cut to leave room for the header, the
// status bar and a line of transcript. A frame taller than the window makes
// the terminal scroll, and every later frame lands in the wrong place.
func (m *Model) bottomFitted() string {
	b := m.bottomView()
	if lines := strings.Split(b, "\n"); len(lines) > m.bottomRoom() {
		// bottomView keeps the input box whole, so this only fires when the box
		// itself is taller than the room there is. Take whole rows off the top
		// rather than keeping the end, which would leave half a border behind.
		b = strings.Join(lines[len(lines)-m.bottomRoom():], "\n")
	}
	return b
}

type headerKey struct {
	w      int
	sideW  int
	engine string
	cwd    string
	branch string
	model  string
	effort string
	theme  string
	agents int
}

func (m *Model) headerModelLabel() string {
	if m.model == "" {
		return ""
	}
	label := sText.Render(shortModel(m.model))
	if m.settings.Effort != "" {
		lvl, _ := m.effortInUse()
		label += sFaint.Render(" · ") + sAccent2.Render(lvl)
	}
	return label
}

func (m *Model) header() string {
	eff := ""
	if m.settings.Effort != "" {
		eff, _ = m.effortInUse()
	}
	key := headerKey{w: m.w, sideW: m.sideW, engine: m.engine, cwd: m.cwd, branch: m.branch, model: m.model, effort: eff, theme: theme.Name, agents: m.activeSubagents()}
	if m.headerCache != "" && key == m.headerKeyUsed {
		return m.headerCache
	}
	brand := " " + sAccent.Render("✦") + " " + sBold.Render("teveus")
	switch m.engine {
	case "claude":
		brand += " " + chip("Claude", cAccent)
	case "api":
		brand += " " + chip("API", cAccent2)
	case "antigravity":
		brand += " " + chip("Agy", cBlue)
	}
	if m.sideW > 0 {
		// Left header carries brand, cwd, branch, active subagents and model
		// across the main reading canvas; the right rail header holds ? help.
		pw := m.paneW()
		padL := strings.Repeat(" ", m.paneMargin())
		lLeft := padL + strings.TrimPrefix(brand, " ")
		if pw >= 56 {
			lLeft += "  " + sDim.Render(shortPath(m.cwd))
		}
		if m.branch != "" && pw >= 72 {
			lLeft += "  " + sFaint.Render("⎇ ") + sAccent2.Render(m.branch)
		}
		var lparts []string
		if active := m.activeSubagents(); active > 0 {
			lparts = append(lparts, chip(fmt.Sprintf("◇ %s", plural(active, "%d agent")), cAccent2))
		}
		if m.model != "" {
			lparts = append(lparts, m.headerModelLabel())
		}
		lRight := strings.Join(lparts, sFaint.Render("  ")) + padL
		gap := pw - lipgloss.Width(lLeft) - lipgloss.Width(lRight)
		if gap < 1 {
			lRight = ""
			if m.model != "" {
				lRight = sText.Render(shortModel(m.model)) + " "
			}
			gap = max(pw-lipgloss.Width(lLeft)-lipgloss.Width(lRight), 1)
		}
		leftCell := lLeft + strings.Repeat(" ", gap) + lRight
		if w := lineWidth(leftCell); w > pw {
			leftCell = ansi.Truncate(leftCell, pw, "")
		} else if w < pw {
			leftCell += strings.Repeat(" ", pw-w)
		}

		railInner := max(m.sideW-1, 4)
		helpStr := sFaint.Render("? help  ")
		rightPad := max(railInner-lipgloss.Width(helpStr), 0)
		rightCell := strings.Repeat(" ", rightPad) + helpStr
		m.headerCache, m.headerKeyUsed = leftCell+sBorder.Render("│")+rightCell, key
		return m.headerCache
	}

	left := brand
	if m.w >= 64 {
		left += "  " + sDim.Render(shortPath(m.cwd))
		if m.branch != "" {
			left += "  " + sFaint.Render("⎇ ") + sAccent2.Render(m.branch)
		}
	}
	var rparts []string
	if m.model != "" {
		rparts = append(rparts, m.headerModelLabel())
	}
	if m.w >= 96 {
		rparts = append(rparts, sFaint.Render("? help"))
	}
	right := strings.Join(rparts, sFaint.Render("  ")) + " "
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = ""
		if m.model != "" {
			right = sText.Render(shortModel(m.model)) + " "
		}
		gap = max(m.w-lipgloss.Width(left)-lipgloss.Width(right), 1)
	}
	m.headerCache, m.headerKeyUsed = left+strings.Repeat(" ", gap)+right, key
	return m.headerCache
}

// headerRule draws the architectural hairline crossbar, placing a clean ┼
// intersection exactly at the Right Rail divider column.
func (m *Model) headerRule() string {
	if m.ruleCache != "" && m.ruleCacheW == m.w && m.ruleCacheSide == m.sideW && m.ruleCacheThm == theme.Name {
		return m.ruleCache
	}
	var line string
	if m.sideW > 0 && m.sideW < m.w {
		line = strings.Repeat("─", m.chatW) + "┼" + strings.Repeat("─", max(m.sideW-1, 0))
	} else {
		line = strings.Repeat("─", max(m.w, 1))
	}
	m.ruleCache = sBorder.Render(line)
	m.ruleCacheW, m.ruleCacheSide, m.ruleCacheThm = m.w, m.sideW, theme.Name
	return m.ruleCache
}

func (m *Model) level() string {
	if m.settings.Level == "" {
		return "standard"
	}
	return m.settings.Level
}

func (m *Model) welcome(width int) string {
	var lines []string
	greeting := "Quiet speed. Minimal tokens."
	if p := m.project; p.language != "" && p.name != "" && p.name != "." {
		greeting = "What should we build in " + truncate(p.name, max(width-36, 8)) + " today?"
	}
	if m.engine == "api" && m.model != "" && m.sideW == 0 {
		greeting += sFaint.Render("  ·  " + shortModel(m.model))
	}
	if m.vp.Height >= 15 && width >= 46 {
		// Swiss-editorial architectural monogram beside spaced wordmark.
		emblem := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cAccent).
			Padding(0, 1).
			Render(sAccent.Bold(true).Render("✦"))
		titleStack := lipgloss.JoinVertical(lipgloss.Left,
			gradient("t e v e u s", cAccent, cAccent2, true),
			sDim.Render(greeting),
		)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Center, emblem, "  ", titleStack))
	} else {
		if m.vp.Height >= 12 {
			lines = append(lines, sAccent.Bold(true).Render("✦")+"  "+gradient("t e v e u s", cAccent, cAccent2, true))
		}
		lines = append(lines, sDim.Render(greeting))
	}

	// Auto-detected project stack chips (Law of Similarity + immediate context).
	var stack []string
	if m.project.language != "" {
		stack = append(stack, chip(m.project.language, cBlue))
	}
	if m.project.test != "" {
		stack = append(stack, chip(m.project.test, cGreen))
	}
	if m.branch != "" {
		stack = append(stack, chip("⎇ "+m.branch, cAccent2))
	}
	if len(stack) > 0 && width >= 44 {
		row := strings.Join(stack, sFaint.Render(" · "))
		if ansi.StringWidth(row) <= width-2 {
			lines = append(lines, "", row)
		}
	}
	lines = append(lines, "")

	if m.engine == "" {
		// Nothing runs until the user chooses where the AI comes from.
		lines = append(lines, sText.Render("Connect an AI to start:  ")+keycap("/login"), "")
	}

	if m.level() == "pro" {
		lines = append(lines, sFaint.Render("? help  ·  ctrl+k palette  ·  /saver presets  ·  /settings"))
		return m.center(width, lines)
	}

	// 2x2 Bento starter grid on wide desktop terminals (distinctive editorial
	// layout), framed Quick Start card on medium widths, compact list on tiny screens.
	starters := m.starterList()
	if width >= 82 && m.vp.Height >= 19 && len(starters) >= 4 {
		cardW := min((width-4)/2, 42)
		inner := cardW - 4
		bento := func(i int, text string) string {
			num := chip(fmt.Sprint(i+1), cAccent)
			arrow := sFaint.Render("→")
			topGap := max(inner-ansi.StringWidth(num)-ansi.StringWidth(arrow), 1)
			top := num + strings.Repeat(" ", topGap) + arrow
			wrapped := strings.Split(ansi.Wrap(termSafe(text), inner, ""), "\n")
			l1, l2 := "", ""
			if len(wrapped) > 0 {
				l1 = truncate(wrapped[0], inner)
			}
			if len(wrapped) > 1 {
				l2 = truncate(wrapped[1], inner)
			}
			body := top + "\n" + sText.Render(l1) + "\n" + sText.Render(l2)
			return lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(cBorder).
				Padding(0, 1).
				Width(cardW - 2).
				Render(body)
		}
		row1 := lipgloss.JoinHorizontal(lipgloss.Top, bento(0, starters[0]), "  ", bento(1, starters[1]))
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, bento(2, starters[2]), "  ", bento(3, starters[3]))
		lines = append(lines, row1, row2, sFaint.Render("Press 1–4 to pick a card, or type your own prompt"), "")
	} else if width >= 54 && m.vp.Height >= 19 {
		cardW := min(width-4, 70)
		inner := cardW - 4
		var cardRows []string
		cardRows = append(cardRows, sDim.Bold(true).Render("QUICK START"))
		for i, s := range starters {
			num := chip(fmt.Sprint(i+1), cAccent)
			txt := truncate(s, max(inner-8, 8))
			gap := max(inner-ansi.StringWidth(num)-2-ansi.StringWidth(txt)-2, 1)
			cardRows = append(cardRows, num+"  "+sText.Render(txt)+strings.Repeat(" ", gap)+sFaint.Render("→"))
		}
		cardRows = append(cardRows, "", sFaint.Render("Press 1–4 to start, or type your own prompt"))
		card := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1).
			Width(cardW - 2).
			Render(strings.Join(cardRows, "\n"))
		lines = append(lines, card, "")
	} else {
		var rows []string
		for i, s := range starters {
			num := chip(fmt.Sprint(i+1), cAccent)
			rows = append(rows, num+"  "+sText.Render(truncate(s, max(width-8, 8))))
		}
		lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, rows...), "")
	}

	if width >= 64 {
		lines = append(lines, hints("ctrl+k", "palette", "@", "files", "/", "commands", "shift+tab", "mode"))
		lines = append(lines, sFaint.Render("? all shortcuts   ·   /saver token presets   ·   ctrl+b rail"))
	} else {
		lines = append(lines, hints("ctrl+k", "palette", "@", "files"))
		lines = append(lines, sFaint.Render("? shortcuts   ·   /settings"))
	}
	if m.level() == "guided" {
		lines = append(lines, "", sFaint.Render("Type a request in plain words and press enter. Every change asks first."))
	}
	return m.center(width, lines)
}

func (m *Model) center(width int, lines []string) string {
	if v := m.cfg.Version; v != "" {
		lines = append(lines, "", sFaint.Render("teveus "+v))
	}
	block := lipgloss.JoinVertical(lipgloss.Center, lines...)
	top := max((m.vp.Height-lipgloss.Height(block))/2, 0)
	return strings.Repeat("\n", top) + lipgloss.PlaceHorizontal(width, lipgloss.Center, block)
}

func (m *Model) sidebarLines(height int) []string {
	_ = m.sidebar(height)
	return m.sideLines
}

func (m *Model) sidebar(height int) string {
	// Everything the Left Rail shows — model, mode, cost, tokens, usage bars —
	// stays the same while a reply streams, but the frame is redrawn many
	// times a second for the spinner. Cached on those values the redraws are
	// free, and comparing them is cheaper than building the section stack.
	sideW := m.sideW
	if sideW < 12 {
		sideW = min(sidebarWidth, max(m.w, 12))
	}
	mcpActive, mcpTools := 0, 0
	if eng, ok := m.client.(*agent.Engine); ok {
		for _, st := range eng.MCPStatus() {
			if st.State == "connected" && !st.Disabled {
				mcpActive++
				mcpTools += len(st.Tools)
			}
		}
	}
	savLabel := m.saverLabel()
	subActive, subToks := 0, 0
	subLastStep := ""
	for _, sub := range m.subagents {
		if !sub.done {
			subActive++
		}
		subToks += sub.tokIn + sub.tokOut
		subLastStep = sub.step
	}
	touchedSig := ""
	if len(m.touchedFiles) > 0 {
		tf := m.touchedFiles[0]
		touchedSig = fmt.Sprintf("%d:%s:%d:%d", len(m.touchedFiles), tf.path, tf.added, tf.removed)
	}
	key := sideKey{
		height: height, sideW: sideW, model: m.model, mode: m.mode,
		engine: m.engine, cost: m.cost, tokIn: m.tokIn, tokOut: m.tokOut,
		tokCache: m.tokCache, turnIn: m.turnIn, turnOut: m.turnOut,
		lastTurnCost: m.lastTurnCost,
		context:      m.context, contextMax: m.contextMax, planTier: m.planTier,
		busy: m.busy, perms: len(m.perms),
		queue: len(m.queue), tasks: len(m.tasks), todos: len(m.todos),
		sessionID: m.sessionID, cwd: m.cwd, branch: m.branch,
		five: m.fiveHour != nil, seven: m.sevenDay != nil, month: m.monthly != nil,
		theme: theme.Name, saver: savLabel, effort: m.settings.Effort,
		skills: m.skillCount, mcpActive: mcpActive, mcpTools: mcpTools,
		projLang: m.project.language, projTest: m.project.test,
		blocks: len(m.blocks), subCount: len(m.subagents), subActive: subActive,
		subToks: subToks, subStep: subLastStep, touchedSig: touchedSig,
	}
	if m.sideCache != "" && key == m.sideKeyUsed {
		return m.sideCache
	}
	// 2-cell left/right padding inside the Left Rail gives Swiss breathing room.
	padX := 2
	if sideW < 24 {
		padX = 1
	}
	w := max(sideW-1-padX*2, 6)
	var s []string
	// Hairline divider separates major functional clusters (Law of Common Region).
	rule := sBorder.Render(strings.Repeat("─", w))
	section := func(title, right string) {
		if len(s) > 0 {
			s = append(s, "", rule)
		}
		left := sFaint.Bold(true).Render(strings.ToUpper(title))
		if right == "" {
			s = append(s, left)
			return
		}
		gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(right), 1)
		s = append(s, left+strings.Repeat(" ", gap)+right)
	}
	// Swiss two-column row: muted label flush-left, value flush-right.
	kv := func(k, v string) string {
		lk := sDim.Render(k)
		gap := max(w-ansi.StringWidth(lk)-ansi.StringWidth(v), 1)
		return lk + strings.Repeat(" ", gap) + v
	}

	section("SESSION", sFaint.Render(func() string {
		if m.sessionID == "" {
			return ""
		}
		id := strings.TrimPrefix(m.sessionID, "ses_")
		return id[:min(6, len(id))]
	}()))
	if m.model != "" {
		s = append(s, kv("model", sText.Render(truncate(shortModel(m.model), max(w-7, 4)))))
	}
	s = append(s, kv("mode", lipgloss.NewStyle().Foreground(modeColor(m.mode)).Render("● "+modeLabel(m.mode))))
	if m.settings.Effort != "" {
		lvl, capped := m.effortInUse()
		v := sAccent2.Render(lvl)
		if capped {
			v += sFaint.Render(" (" + m.settings.Effort + " capped)")
		}
		s = append(s, kv("effort", v))
	}
	liveIn, liveOut, liveCache := m.tokIn+m.turnIn, m.tokOut+m.turnOut, m.tokCache+m.turnCache
	if m.engine != "api" || m.cost > 0 {
		costVal := sText.Render(fmt.Sprintf("$%.3f", m.cost))
		if m.lastTurnCost > 0 && m.lastTurnCost < m.cost {
			costVal += sFaint.Render(fmt.Sprintf(" +%.3f", m.lastTurnCost))
		}
		s = append(s, kv("cost", costVal))
	}
	if liveIn+liveOut > 0 || (m.engine == "api" && m.cost == 0) {
		tokVal := sText.Render(truncate("↑"+fmtTokens(liveIn)+" · ↓"+fmtTokens(liveOut), max(w-7, 4)))
		s = append(s, kv("tokens", tokVal))
		if liveCache > 0 && liveIn > 0 {
			if pct := min(liveCache*100/liveIn, 100); pct > 0 {
				s = append(s, kv("cache", sAccent.Render(fmt.Sprintf("%d%% hit", pct))))
			}
		}
	}
	if m.context > 0 {
		ctxCol := sDim
		ctxLimit := 200000.0
		if m.contextMax > 0 {
			ctxLimit = float64(m.contextMax)
		}
		frac := float64(m.context) / ctxLimit
		if m.context > contextWarn || frac >= 0.75 {
			ctxCol = sYellow
		}
		right := ctxCol.Render(fmt.Sprintf("%.1fk", float64(m.context)/1000)) + sFaint.Render(fmt.Sprintf(" %2.0f%%", min(frac*100, 100)))
		barW := max(w-4-ansi.StringWidth(right)-2, 4)
		s = append(s, sDim.Render("ctx ")+bar(frac, barW)+" "+right)
	}

	// Live Subagent Monitor (Progressive Disclosure: shown once a subagent runs).
	if len(m.subagents) > 0 {
		tag := sFaint.Render(fmt.Sprintf("%d done", len(m.subagents)))
		if subActive > 0 {
			tag = sAccent2.Render(fmt.Sprintf("%d active", subActive))
		}
		section("AGENTS", tag)
		shown := 0
		for i := len(m.subagents) - 1; i >= 0 && shown < 3; i-- {
			sub := m.subagents[i]
			shown++
			var icon string
			switch {
			case !sub.done:
				icon = sAccent2.Render("◐")
			case sub.failed:
				icon = sRed.Render("✗")
			default:
				icon = sGreen.Render("◇")
			}
			tot := sub.tokIn + sub.tokOut
			if !sub.done {
				s = append(s, truncate(icon+" "+sText.Render(sub.desc), w))
				detail := sub.step
				if tot > 0 {
					detail += " · " + fmtTokens(tot)
				}
				s = append(s, truncate(sBorder.Render(" └ ")+sDim.Render(detail), w))
			} else {
				tokStr := ""
				if tot > 0 {
					tokStr = sFaint.Render(fmtTokens(tot))
				}
				left := icon + " " + sDim.Render(truncate(sub.desc, max(w-ansi.StringWidth(tokStr)-3, 6)))
				if tokStr != "" {
					gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(tokStr), 1)
					s = append(s, left+strings.Repeat(" ", gap)+tokStr)
				} else {
					s = append(s, left)
				}
			}
		}
	}

	// Touched Files Ledger (Progressive Disclosure: shown after edits/writes).
	if len(m.touchedFiles) > 0 {
		section("FILES", sFaint.Render(fmt.Sprint(len(m.touchedFiles))))
		for i, tf := range m.touchedFiles {
			if i >= 4 {
				s = append(s, sFaint.Render(fmt.Sprintf("+%d more", len(m.touchedFiles)-4)))
				break
			}
			act := sYellow.Render("M")
			if tf.action == "+" {
				act = sGreen.Render("+")
			}
			delta := ""
			if tf.added > 0 {
				delta += sGreen.Render(fmt.Sprintf("+%d", tf.added))
			}
			if tf.removed > 0 {
				if delta != "" {
					delta += " "
				}
				delta += sRed.Render(fmt.Sprintf("-%d", tf.removed))
			}
			name := filepath.Base(tf.path)
			left := act + " " + sText.Render(truncate(name, max(w-ansi.StringWidth(delta)-3, 6)))
			if delta != "" {
				gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(delta), 1)
				s = append(s, left+strings.Repeat(" ", gap)+delta)
			} else {
				s = append(s, left)
			}
		}
	}

	// Compact Token Savers row: 1 crisp line instead of stacked boxes.
	if savLabel == "off" || savLabel == "off (-full)" {
		section("SAVERS", sFaint.Render("/saver"))
		s = append(s, kv("preset", sDim.Render(savLabel)))
	} else {
		saveTag := ""
		if m.settings.LeanTools && (m.engine == "claude" || m.engine == "api") {
			saveTag = sGreen.Render("-lean")
			if m.engine == "claude" {
				saveTag = sGreen.Render("-2.2k/req")
			}
		}
		section("SAVERS", saveTag)
		var chips []string
		for _, it := range strings.Split(savLabel, " · ") {
			chips = append(chips, softPill(it, cDiffAdd, cGreen))
		}
		row := ""
		for _, ch := range chips {
			if row != "" && ansi.StringWidth(row)+1+ansi.StringWidth(ch) > w {
				s = append(s, row)
				row = ""
			}
			if row != "" {
				row += " "
			}
			row += ch
		}
		if row != "" {
			s = append(s, row)
		}
	}

	// Goal gradient: a visible finish line keeps a long task legible.
	tasks := m.tasks
	if len(tasks) == 0 {
		tasks = todoTasks(m.todos)
	}
	if len(tasks) > 0 {
		done := 0
		for _, t := range tasks {
			if t.status == "completed" {
				done++
			}
		}
		section("PLAN", sFaint.Render(fmt.Sprintf("%d/%d", done, len(tasks))))
		s = append(s, bar(float64(done)/float64(len(tasks)), w))
		for _, t := range taskLines(tasks) {
			s = append(s, truncate(t, w))
		}
	}

	if m.fiveHour != nil || m.sevenDay != nil || m.monthly != nil {
		section("QUOTA", "")
		if m.fiveHour != nil {
			s = append(s, usageLineCompact("5h", m.fiveHour, w)...)
		}
		if m.sevenDay != nil {
			s = append(s, usageLineCompact("7d", m.sevenDay, w)...)
		}
		if m.monthly != nil {
			s = append(s, usageLineCompact("30d", m.monthly, w)...)
		}
	} else if m.planTier != "" || m.contextMax > 0 {
		section("USAGE", sFaint.Render(truncate(m.planTier, max(w-7, 4))))
		ctxLimit := 200000.0
		if m.contextMax > 0 {
			ctxLimit = float64(m.contextMax)
		}
		frac := float64(m.context) / ctxLimit
		pct := fmt.Sprintf(" %3.0f%%", min(frac*100, 100))
		barW := max(w-4-len(pct), 4)
		s = append(s, sDim.Render("ctx ")+bar(frac, barW)+sText.Render(pct))
		if m.contextMax > 0 {
			s = append(s, sFaint.Render(fmt.Sprintf("    %s / %s", fmtTokens(m.context), fmtTokens(m.contextMax))))
		}
		if liveIn > 0 && liveCache > 0 {
			cFrac := min(float64(liveCache)/float64(liveIn), 1.0)
			cPct := fmt.Sprintf(" %3.0f%%", cFrac*100)
			cBarW := max(w-4-len(cPct), 4)
			s = append(s, sDim.Render("hit ")+bar(cFrac, cBarW)+sAccent.Render(cPct))
		}
	}

	var recent []*block
	for i := len(m.blocks) - 1; i >= 0 && len(recent) < 4; i-- {
		if m.blocks[i].kind == kindTool {
			recent = append(recent, m.blocks[i])
		}
	}
	if len(recent) > 0 && len(s)+len(recent)+3 <= height {
		section("ACTIVITY", "")
		for _, b := range recent {
			icon := sGreen.Render("✓")
			switch b.state {
			case toolRunning:
				icon = sAccent.Render(m.r.spin)
			case toolFailed:
				icon = sRed.Render("✗")
			}
			line := icon + " " + sText.Render(displayName(b.name))
			if sum := m.r.summary(b.name, b.input); sum != "" {
				line += " " + sDim.Render(sum)
			}
			s = append(s, truncate(line, w))
		}
	} else if len(recent) == 0 {
		var ws []string
		if m.branch != "" {
			ws = append(ws, kv("git", sAccent2.Render("⎇ "+truncate(m.branch, max(w-7, 4)))))
		}
		if m.project.language != "" {
			stack := m.project.language
			if m.project.test != "" {
				stack += " · " + m.project.test
			}
			ws = append(ws, kv("stack", sText.Render(truncate(stack, max(w-7, 4)))))
		}
		if mcpActive > 0 {
			ws = append(ws, kv("mcp", sText.Render(fmt.Sprintf("%d active", mcpActive))))
		}
		if m.skillCount > 0 {
			ws = append(ws, kv("skills", sText.Render(fmt.Sprint(m.skillCount))))
		}
		if len(ws) > 0 {
			section("WORKSPACE", "")
			s = append(s, ws...)
		}
	}

	// Left border sits on exact column m.chatW; 2-cell horizontal padding
	// keeps the right rail airy and calm.
	out := lipgloss.NewStyle().
		Width(sideW-1).Height(height).MaxHeight(height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(cBorder).
		Padding(0, padX).
		Render(fitWidth(strings.Join(s, "\n"), w))
	m.sideCache, m.sideLines, m.sideKeyUsed = out, strings.Split(out, "\n"), key
	return out
}

// sideKey is what the sidebar depends on: everything its sections print.
type sideKey struct {
	height, sideW, perms, queue, tasks, todos    int
	model, mode, engine                          string
	cost, lastTurnCost                           float64
	tokIn, tokOut, tokCache, context, contextMax int
	planTier                                     string
	turnIn, turnOut                              int
	busy                                         bool
	sessionID, cwd, branch                       string
	five, seven, month                           bool
	theme, saver, effort                         string
	skills, mcpActive, mcpTools, blocks          int
	projLang, projTest                           string
	subCount, subActive, subToks                 int
	subStep, touchedSig                          string
}

func todoTasks(input map[string]any) []task {
	todos, _ := input["todos"].([]any)
	var out []task
	for _, t := range todos {
		m, _ := t.(map[string]any)
		content, _ := m["content"].(string)
		status, _ := m["status"].(string)
		out = append(out, task{subject: content, status: status})
	}
	return out
}

// usageLineCompact keeps the Left Rail airy: shows reset time only when a
// quota window is getting tight (>= 70%), avoiding 3 repetitive timestamp rows.
func usageLineCompact(name string, win *claude.Window, w int) []string {
	label := fmt.Sprintf("%-3s ", name)
	pct := fmt.Sprintf(" %3.0f%%", win.Utilization*100)
	barW := max(w-len(label)-len(pct), 4)
	lines := []string{sDim.Render(label) + bar(win.Utilization, barW) + sText.Render(pct)}
	if win.ResetsAt > 0 && win.Utilization >= 0.70 {
		lines = append(lines, sFaint.Render("    resets "+time.Unix(win.ResetsAt, 0).Format("Mon 15:04")))
	}
	return lines
}

func (m *Model) bottomView() string {
	if m.pop.mode == popReview {
		// The review needs the room the input would take.
		return m.popupView()
	}
	// The box the user types in is the part that must survive: it carries the
	// permission mode and the keys to answer, and half of it is worse than none.
	// Everything above it (the popup, the queue) is what gives way when the
	// window is short, so a tall popup can never cut the input in two.
	var below []string
	if len(m.perms) > 0 {
		below = append(below, m.permView())
	} else {
		if q := m.queueView(); q != "" {
			below = append(below, q)
		}
		below = append(below, m.inputView())
	}
	keep := strings.Join(below, "\n")
	room := m.bottomRoom() - lipgloss.Height(keep)
	if !m.pop.open() || room < 1 {
		return keep
	}
	var above []string
	if p := m.popupView(); room > 0 {
		if lines := strings.Split(p, "\n"); len(lines) > room {
			p = strings.Join(lines[:room], "\n")
		}
		above = append(above, p)
	}
	if len(above) == 0 {
		return keep
	}
	return lipgloss.JoinVertical(lipgloss.Left, append(above, keep)...)
}

// bottomRoom is the height the bottom area may take: everything but the
// header, its hairline divider, the tab strip, the status bar and a line of transcript.
func (m *Model) bottomRoom() int {
	return max(m.h-m.topHeight()-2, 1)
}

// queueView lists messages waiting for the current turn, faint and just
// above the input they came from. It takes no space when the queue is empty.
func (m *Model) queueView() string {
	pw := m.paneW()
	var out []string
	for i, q := range m.queue {
		if i == 2 && len(m.queue) > 3 {
			out = append(out, sFaint.Render(fmt.Sprintf(" … %d more queued", len(m.queue)-2)))
			break
		}
		q = strings.Join(strings.Fields(q), " ")
		out = append(out, sFaint.Render(" › "+truncate(q, max(pw-30, 8))))
	}
	if len(out) > 0 {
		out[0] += sFaint.Render("  click or ctrl+e to edit")
	}
	return strings.Join(out, "\n")
}

// inputView draws the floating dock composer inside the editorial pane, with
// the permission mode set into its bottom border.
func (m *Model) inputView() string {
	pw := m.paneW()
	li := m.input.LineInfo()
	key := m.input.Value() + "\x00" + m.mode + "\x00" + theme.Name + "\x00" +
		fmt.Sprint(pw, "/", m.input.Line(), "/", li.CharOffset, "/", li.ColumnOffset, "/", m.input.Height())
	if m.inputCache != "" && key == m.inputKey {
		return m.inputCache
	}
	col := modeColor(m.mode)
	frameCol := cBorder
	if m.mode != "" && m.mode != "default" {
		// Error Prevention: elevate border tint when auto-edit/plan/autopilot is active.
		frameCol = col
	}
	bc := lipgloss.NewStyle().Foreground(frameCol)
	label := " " + lipgloss.NewStyle().Foreground(col).Bold(true).Render("● "+modeLabel(m.mode)) + " "
	right := sFaint.Render(" shift+tab mode · enter send ")
	// Align floating composer to the exact same left/right margin as the
	// editorial transcript (Law of Continuity).
	margin := ""
	boxW := pw
	if m.sideW > 0 && pw >= 24 {
		mg := m.paneMargin()
		margin = strings.Repeat(" ", mg)
		boxW = pw - mg*2
	}
	w := max(boxW-2, 10)
	// The body is w wide plus its two side borders; the bottom line must
	// match: "╰─" + label + fill + right + "─╯".
	fill := w + 2 - 4 - lipgloss.Width(label) - lipgloss.Width(right)
	if fill < 1 {
		right = sFaint.Render(" shift+tab ")
		fill = w + 2 - 4 - lipgloss.Width(label) - lipgloss.Width(right)
	}
	if fill < 0 {
		right = ""
		fill = max(w+2-4-lipgloss.Width(label), 0)
	}
	top := margin + bc.Render("╭"+strings.Repeat("─", w)+"╮") + margin
	bottom := margin + bc.Render("╰─") + label + bc.Render(strings.Repeat("─", fill)) + right + bc.Render("─╯") + margin
	bodyRaw := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, false, true).
		BorderForeground(frameCol).
		Padding(0, 1).
		Width(w).
		Render(fitWidth(m.input.View(), max(w-2, 1)))
	var bodyLines []string
	for _, bl := range strings.Split(bodyRaw, "\n") {
		bodyLines = append(bodyLines, margin+bl+margin)
	}
	m.inputCache, m.inputKey = top+"\n"+strings.Join(bodyLines, "\n")+"\n"+bottom, key
	return m.inputCache
}

// permDiffVisible reports whether the diff for this Edit/Write permission
// request is already visible right above the bottom prompt, so permView can
// stay compact instead of showing the same diff twice.
func (m *Model) permDiffVisible(p *claude.PermissionRequest) bool {
	if m.permFullDiff || p.ToolUseID == "" || !m.vp.AtBottom() {
		return false
	}
	switch p.ToolName {
	case "Edit", "MultiEdit", "Write":
	default:
		return false
	}
	tb := m.tools[p.ToolUseID]
	for i := max(len(m.blocks)-3, 0); i < len(m.blocks); i++ {
		if b := m.blocks[i]; b == tb || (b.kind == kindTool && b.id == p.ToolUseID) {
			return true
		}
	}
	return false
}

func (m *Model) permView() string {
	pw := m.paneW()
	p := m.perms[0]
	var in map[string]any
	json.Unmarshal(p.Input, &in)
	mg := m.paneMargin()
	boxW := max(pw-mg*2-2, 16)
	inner := max(boxW-4, 12)
	if pw < 24 {
		mg = 0
		boxW = max(pw-2, 14)
		inner = max(boxW-4, 8)
	}

	title := sYellow.Bold(true).Render("⚠  Approval needed") + "   " + sBold.Render(displayName(p.ToolName))
	if p.Description != "" {
		room := inner - lipgloss.Width(title) - 3
		if room > 8 {
			title += "  " + sDim.Render(truncate(p.Description, room))
		}
	}
	compact := m.permDiffVisible(p)
	if compact {
		room := inner - lipgloss.Width(title) - 3
		if room > 18 {
			title += sFaint.Render("  ·  diff highlighted above")
		}
	}
	// Leave the prompt's frame, title and keys (6 lines), the header and
	// status bar, and a few transcript lines on screen.
	budget := max(m.h-14, 2)
	var body []string
	if !compact {
		switch p.ToolName {
		case "Bash":
			cmd, _ := in["command"].(string)
			body = clip(wrapLines("$ "+cmd, inner, sText), min(8, budget))
		case "Edit", "MultiEdit":
			if d, ok := in["diff"].(string); ok {
				body = clip(unifiedDiff(d, inner), min(12, budget))
			} else {
				body = clip(m.r.editDiff(in, inner), min(12, budget))
			}
		case "Write":
			content, _ := in["content"].(string)
			body = clip(diffLines("", content, inner), min(10, budget))
		default:
			b, _ := json.MarshalIndent(in, "", "  ")
			body = clip(wrapLines(string(b), inner, sDim), min(8, budget))
		}
	}
	opts := []string{pill(" y ", cGreen) + sText.Render(" allow once")}
	if hasSuggestions(p) {
		always := "always allow"
		if p.AlwaysLabel != "" {
			always = p.AlwaysLabel
		}
		opts = append(opts, pill(" a ", cAccent2)+sText.Render(" "+termSafe(always)))
	}
	opts = append(opts, pill(" n ", cRed)+sText.Render(" deny"))
	if p.ToolName == "Edit" || p.ToolName == "MultiEdit" || p.ToolName == "Write" {
		if compact {
			opts = append(opts, chip("d", cDim)+sDim.Render(" full diff"))
		} else if m.permFullDiff {
			opts = append(opts, chip("d", cDim)+sDim.Render(" compact"))
		}
	}
	keys := strings.Join(opts, "   ")
	if len(m.perms) > 1 {
		keys += sFaint.Render(fmt.Sprintf("   +%d more waiting", len(m.perms)-1))
	}
	var contentLines []string
	if len(body) > 0 {
		contentLines = append(append([]string{title, ""}, body...), "", keys)
	} else {
		contentLines = []string{title, "", keys}
	}
	permBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cYellow).
		Padding(0, 1).
		Width(boxW)
	if mg > 0 {
		permBox = permBox.MarginLeft(mg)
	}
	return permBox.Render(strings.Join(contentLines, "\n"))
}

// statusBar: what is happening on the left, what you can do right now on
// the right. Hints change with context so there are never more than a few.
func (m *Model) statusBar() string {
	pw := m.paneW()
	var left, right string
	switch {
	case len(m.perms) > 0:
		left = sYellow.Bold(true).Render(" ● waiting for your approval")
		right = hints("y", "allow", "a", "always", "n", "deny")
	case m.pop.mode == popMention:
		left = sDim.Render(" @ files")
		right = hints("↑↓", "select", "tab", "insert", "esc", "close")
	case m.pop.mode == popSlider:
		left = sAccent.Render(" ›") + sDim.Render(" adjust")
		right = hints("← →", "adjust", "enter", "save", "esc", "cancel")
	case m.pop.mode == popReview:
		left = sAccent.Render(" ›") + sDim.Render(" review · nothing changes yet")
		right = hints("↑↓", "file", "enter", "restore all", "esc", "back")
	case m.pop.open():
		left = sAccent.Render(" ›") + sDim.Render(" choose")
		right = hints("↑↓", "select", "enter", "run", "esc", "close")
	case m.busy:
		phase := shimmer(m.phase+"…", m.frame)
		if m.settings.ReduceMotion {
			phase = sAccent.Render(m.phase + "…")
		}
		left = " " + sAccent.Render(m.r.spin) + " " + phase +
			sDim.Render("  "+fmtDur(time.Since(m.turnStart)))
		if active := m.activeSubagents(); active > 0 {
			left += sFaint.Render(" · ") + sAccent2.Render("◇ "+plural(active, "%d agent"))
		}
		if m.turnIn+m.turnOut > 0 {
			left += sFaint.Render(" · ") + sDim.Render("↑"+fmtTokens(m.turnIn)+" ↓"+fmtTokens(m.turnOut))
		}
		right = hints("esc×2", "stop", "ctrl+o", "details")
	case m.engine == "":
		left = sFaint.Render(" ○") + sDim.Render(" not connected")
		right = hints("/login", "connect", "ctrl+k", "palette")
	default:
		// Distinct ✦ ready avoids repeating a second ● dot directly under ● ask first.
		left = sAccent.Render(" ✦") + sDim.Render(" ready")
		if pw >= 76 && m.engine != "" {
			left += sFaint.Render(" · " + engineLabel(m.engine))
		}
		if pw >= 84 && m.lastTurnIn+m.lastTurnOut > 0 {
			left += sFaint.Render(" · last ↑" + fmtTokens(m.lastTurnIn) + " ↓" + fmtTokens(m.lastTurnOut))
		}
		if sp := m.spendLine(); m.sideW == 0 && sp != "" {
			// The rail isn't showing what this has cost, so say it here.
			left += sFaint.Render("  ·  " + sp)
		}
		right = hints("ctrl+k", "palette", "ctrl+b", "rail", "?", "help")
	}
	if m.notice != "" && time.Since(m.noticeAt) < 4*time.Second {
		st := sYellow
		if m.noticeOK {
			st = sGreen
		}
		left += "   " + st.Render(m.notice)
	}
	padSide := strings.Repeat(" ", m.paneMargin())
	if !strings.HasPrefix(left, "  ") && m.paneMargin() > 1 {
		left = " " + left
	}
	right += padSide
	gap := pw - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncate(left, pw)
	}
	return truncate(left+strings.Repeat(" ", gap)+right, pw)
}

// spendLine is this session's use, and every session's when there are several.
func (m *Model) spendLine() string {
	here := spendLabel(m.cost, m.tokIn, m.tokOut)
	if all := m.totalSpend(); len(m.sessions) > 1 && all != "" && all != here {
		return strings.TrimSpace(here + " · " + all + " in all")
	}
	return here
}

// shimmer paints text with a bright band sweeping across it using the
// three theme-aware templates pre-baked in styles.go (applyTheme).
func shimmer(s string, frame int) string {
	runes := []rune(s)
	pos := frame%(len(runes)+10) - 3
	var sb strings.Builder
	sb.Grow(len(s) * 6)
	for i, r := range runes {
		d := i - pos
		if d < 0 {
			d = -d
		}
		if d >= 3 {
			sb.WriteRune(r)
			continue
		}
		sh := shimmerShades[d]
		sb.WriteString(sh[:strings.Index(sh, "\x00")])
		sb.WriteRune(r)
		sb.WriteString(sh[strings.Index(sh, "\x00")+1:])
	}
	return sb.String()
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	if len(p) > 40 {
		p = "…/" + filepath.Join(filepath.Base(filepath.Dir(p)), filepath.Base(p))
	}
	return p
}

var modelDate = regexp.MustCompile(`-\d{8}$`)

// shortModel turns "claude-haiku-4-5-20251001" into "haiku-4-5".
func shortModel(id string) string {
	return modelDate.ReplaceAllString(strings.TrimPrefix(id, "claude-"), "")
}
