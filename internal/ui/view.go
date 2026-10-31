package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

const sidebarWidth = 36

// layout sizes the viewport around the header, the bottom area (popup,
// input or permission prompt) and the status bar.
func (m *Model) layout() {
	if m.w == 0 {
		return
	}
	m.sideW = 0
	if !m.settings.HideSide && m.w >= 100 {
		m.sideW = sidebarWidth
	}
	m.chatW = m.w - m.sideW
	m.input.SetWidth(m.w - 4)
	vpH := m.h - 1 - 1 - m.tabsHeight() - lipgloss.Height(m.bottomFitted())
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
	width := min(m.chatW-3, 120) // long lines are hard to read; cap the measure
	// Keep the prefix only while the blocks in it are still the ones it was
	// drawn from and still say the same thing. A cache with no blocks holds
	// only the head (the welcome text), which the first block replaces.
	c := m.tc
	keep := c.width == width && c.level == m.level() && len(c.blocks) <= len(m.blocks) &&
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
			head = m.welcome(m.chatW - 3)
		}
		lines, rest = appendText(lines, "", head)
	}
	// The last block is left out of the cache: it is the one a streamed chunk
	// changes, so the prefix would be stale by the next refresh.
	cut := max(len(m.blocks)-1, 0)
	saved := false
	var next prefixCache
	for i := base; i < len(m.blocks); i++ {
		if i == cut {
			saved = true
			next = prefixCache{nLines: len(lines), nStarts: len(starts), rest: rest,
				width: width, level: m.level()}
		}
		if i > 0 {
			// Proximity: consecutive tool calls sit together, turns breathe.
			// Pro is compact: blank lines only between turns.
			sep := "\n\n"
			switch {
			case m.blocks[i].kind == kindUser:
				// A short rule opens each turn; the user block carries the
				// rest of the visual break.
				sep = "\n\n" + sFaint.Render(strings.Repeat("─", min(width, 28))) + "\n\n"
			case (m.blocks[i-1].kind == kindTool && m.blocks[i].kind == kindTool) || m.level() == "pro":
				sep = "\n"
			}
			lines, rest = appendText(lines, rest, sep)
		}
		starts = append(starts, len(lines))
		lines, rest = appendText(lines, rest, m.r.render(m.blocks[i], width))
	}
	if !saved {
		next = prefixCache{nLines: len(lines), nStarts: len(starts), rest: rest,
			width: width, level: m.level()}
	}
	next.blocks = append(m.tc.blocks[:base], m.blocks[base:cut]...)
	// The unterminated line and the blank line the transcript ends with are
	// only for display: the cache keeps the text they came from, so the next
	// block continues that line instead of starting a second copy of it.
	lines, _ = appendText(lines, rest, "\n")
	m.lines, m.blockStart, m.tc = lines, starts, next
	m.vp.SetLines(m.lines)
	if follow {
		m.vp.GotoBottom()
	}
	if m.phone != nil {
		m.phone.snapshot(m)
	}
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
}

// appendText splits text into transcript lines, continuing any unterminated
// line it started, and returns the unterminated remainder.
func appendText(lines []string, rest, text string) ([]string, string) {
	parts := strings.Split(rest+text, "\n")
	// A one-cell margin, added by hand: lipgloss would measure and pad every
	// line of the whole transcript on each streamed chunk. Tabs are replaced
	// because the terminal's tab stops would skew widths.
	for _, p := range parts[:len(parts)-1] {
		lines = append(lines, " "+strings.ReplaceAll(p, "\t", "    "))
	}
	return lines, parts[len(parts)-1]
}

func (m *Model) View() string {
	if m.w == 0 {
		return ""
	}
	// Size the transcript to what the bottom area needs right now, so a
	// popup opened from anywhere can never push the frame past the window.
	m.layout()
	main := m.chatView()
	if m.sideW > 0 {
		// lipgloss treats Width as a minimum, so a single chat line wider than
		// the column would shift the sidebar right on that row and draw the row
		// past the window, which the terminal then wraps — the border walks
		// sideways and the frame grows a row. Fit the text to the column first.
		main = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(m.chatW).Render(fitWidth(main, m.chatW)), m.sidebar(m.vp.Height))
	}
	rows := []string{m.header()}
	if t := m.tabStrip(); t != "" {
		rows = append(rows, t)
	}
	return m.clampFrame(lipgloss.JoinVertical(lipgloss.Left,
		append(rows, main, m.bottomFitted(), m.statusBar())...))
}

// clampFrame makes the frame exactly as tall as the window with every row
// exactly as wide. A row the terminal draws wider than the layout believes
// wraps, which shifts every row under it sideways and leaves the frame before
// it showing through; a frame taller than the window scrolls the terminal, so
// the next frame lands a row off and the old status bar stays on screen. The
// views are each fitted already — this is the backstop that keeps one mistake
// in any of them from wrecking the whole display.
func (m *Model) clampFrame(s string) string {
	// Decompose Thai and Lao SARA AM first: the width functions count it as one
	// cell, terminals draw two, so measuring the folded form under-reports.
	s = termSafe(s)
	lines := strings.Split(s, "\n")
	pad := strings.Repeat(" ", m.w)
	for i, l := range lines {
		switch w := ansi.StringWidth(l); {
		case w > m.w:
			lines[i] = ansi.Truncate(l, m.w, "")
		case w < m.w:
			lines[i] = l + pad[w:]
		}
	}
	if len(lines) > m.h {
		lines = lines[:m.h]
	}
	for len(lines) < m.h {
		lines = append(lines, pad)
	}
	return strings.Join(lines, "\n")
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

func (m *Model) header() string {
	left := " " + sAccent.Render("✦") + "  " + sBold.Render("teveus")
	if m.engine == "api" {
		left += " " + lipgloss.NewStyle().Background(cSurface).Foreground(cAccent2).Bold(true).
			Padding(0, 1).Render("API")
	}
	sep := sFaint.Render("   ")
	parts := []string{sDim.Render(shortPath(m.cwd))}
	if m.branch != "" {
		parts = append(parts, sFaint.Render("⎇ ")+sAccent2.Render(m.branch))
	}
	if m.model != "" {
		parts = append(parts, sText.Render(shortModel(m.model)))
	}
	right := strings.Join(parts, sep) + " "
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = sText.Render(shortModel(m.model)) + " "
		gap = max(m.w-lipgloss.Width(left)-lipgloss.Width(right), 1)
	}
	return left + strings.Repeat(" ", gap) + right
}

var logo = []string{
	"▀█▀ █▀▀ █ █ █▀▀ █ █ █▀▀",
	" █  ██▄ ▀▄▀ ██▄ █▄█ ▄▄█",
}

func (m *Model) level() string {
	if m.settings.Level == "" {
		return "standard"
	}
	return m.settings.Level
}

func (m *Model) welcome(width int) string {
	var lines []string
	if m.vp.Height >= 16 {
		lines = append(lines, gradient("✦", cAccent, cAccent2, true), "")
		for _, l := range logo {
			lines = append(lines, gradient(l, cAccent, cAccent2, true))
		}
		lines = append(lines, "")
	}
	greeting := "What should we build today?"
	if p := m.project; p.language != "" && p.name != "" && p.name != "." {
		greeting = "What should we build in " + truncate(p.name, max(width-34, 8)) + " today?"
	}
	if m.engine == "api" && m.model != "" {
		greeting += sFaint.Render("  ·  " + shortModel(m.model))
	}
	lines = append(lines, sText.Render(greeting), "")
	if m.engine == "" {
		// Nothing runs until the user chooses where the AI comes from.
		lines = append(lines, sText.Render("Connect an AI to start:  ")+keycap("/login"), "")
	}

	if m.level() == "pro" {
		lines = append(lines, sFaint.Render("? help  ·  ctrl+k palette  ·  /settings"))
		return m.center(width, lines)
	}

	// Starter prompts
	var rows []string
	for i, s := range m.starterList() {
		num := lipgloss.NewStyle().Background(cSel).Foreground(cAccent).Bold(true).Padding(0, 1).Render(fmt.Sprint(i + 1))
		rows = append(rows, num+"  "+sText.Render(s))
	}
	lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, rows...), "")

	if width >= 64 {
		lines = append(lines, hints("ctrl+k", "palette", "@", "files", "/", "commands"))
		lines = append(lines, sDim.Render("? all shortcuts   ·   shift+tab permission mode   ·   /settings"))
	} else {
		lines = append(lines, hints("ctrl+k", "palette", "@", "files"))
		lines = append(lines, sDim.Render("? shortcuts   ·   /settings"))
	}
	if m.level() == "guided" {
		lines = append(lines, "", sFaint.Render("Type a request in plain words and press enter. Claude asks before changing files."))
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

func (m *Model) sidebar(height int) string {
	w := m.sideW - 3
	var s []string
	section := func(title, right string) {
		if len(s) > 0 {
			s = append(s, "")
		}
		t := sDim.Bold(true).Render(strings.ToUpper(title))
		if right != "" {
			t += "  " + sFaint.Render(right)
		}
		s = append(s, t)
	}
	kv := func(k, v string) string {
		return sDim.Render(fmt.Sprintf("%-9s", k)) + v
	}

	section("SESSION", "")
	if m.model != "" {
		s = append(s, kv("model", sText.Render(truncate(shortModel(m.model), w-9))))
	}
	s = append(s, kv("mode", lipgloss.NewStyle().Foreground(modeColor(m.mode)).Render("● "+modeLabel(m.mode))))
	if m.engine == "api" && m.cost == 0 {
		s = append(s, kv("tokens", sText.Render(fmtTokens(m.tokIn)+" in · "+fmtTokens(m.tokOut)+" out")))
	} else {
		s = append(s, kv("cost", sText.Render(fmt.Sprintf("$%.3f", m.cost))))
	}
	if m.context > 0 {
		ctx := sText
		if m.context > contextWarn {
			ctx = sYellow
		}
		s = append(s, kv("context", ctx.Render(fmt.Sprintf("%.1fk tokens", float64(m.context)/1000))))
	}
	if m.settings.Effort != "" {
		// Show what the model is actually asked for, not only what was picked:
		// "max" on a model that stops at "high" would be a lie.
		lvl, capped := m.effortInUse()
		v := sAccent2.Render(lvl)
		if capped {
			v += sFaint.Render(" (" + m.settings.Effort + " capped)")
		}
		s = append(s, kv("effort", v))
	}
	// Wrap the saver list on item boundaries with a hanging indent.
	sav := sGreen
	if m.saverLabel() == "off" {
		sav = sDim
	}
	line, first := "", true
	for _, it := range strings.Split(m.saverLabel(), " · ") {
		if line != "" && 9+lipgloss.Width(line)+3+lipgloss.Width(it) > w {
			s = append(s, kv(map[bool]string{true: "saver", false: ""}[first], sav.Render(line)))
			line, first = "", false
		}
		if line != "" {
			line += " · "
		}
		line += it
	}
	s = append(s, kv(map[bool]string{true: "saver", false: ""}[first], sav.Render(line)))
	if m.sessionID != "" {
		id := strings.TrimPrefix(m.sessionID, "ses_")
		s = append(s, kv("session", sDim.Render(id[:min(8, len(id))])))
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
		section("PLAN", fmt.Sprintf("%d/%d", done, len(tasks)))
		s = append(s, bar(float64(done)/float64(len(tasks)), w))
		for _, t := range taskLines(tasks) {
			s = append(s, truncate(t, w))
		}
	}

	if m.fiveHour != nil || m.sevenDay != nil || m.monthly != nil {
		section("USAGE", "")
		if m.fiveHour != nil {
			s = append(s, usageLine("5h", m.fiveHour, w)...)
		}
		if m.sevenDay != nil {
			s = append(s, usageLine("7d", m.sevenDay, w)...)
		}
		if m.monthly != nil {
			s = append(s, usageLine("30d", m.monthly, w)...)
		}
	}

	var recent []*block
	for i := len(m.blocks) - 1; i >= 0 && len(recent) < 7; i-- {
		if m.blocks[i].kind == kindTool {
			recent = append(recent, m.blocks[i])
		}
	}
	if len(recent) > 0 {
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
	} else if len(m.blocks) > 0 {
		// No tools used yet (the welcome screen already lists these).
		section("SHORTCUTS", "")
		for _, kv := range [][2]string{
			{"ctrl+k", "palette"}, {"@", "add a file"}, {"/", "commands"},
			{"↑ ↓", "history"}, {"ctrl+o", "tool details"}, {"ctrl+y", "copy reply"},
			{"esc", "interrupt"},
		} {
			s = append(s, sText.Render(fmt.Sprintf("%-8s", kv[0]))+" "+sDim.Render(kv[1]))
		}
	}

	// The sidebar builds its lines itself, so fitWidth never sees them. Terminals
	// draw Thai and Lao SARA AM as two cells while the width libraries count one,
	// and a line that wraps on screen pushes the border in a row and everything
	// below it down. Fit the joined rows to the inner width first; wrapping
	// can add rows, so MaxHeight still decides the final height.
	return lipgloss.NewStyle().
		Width(m.sideW-1).Height(height).MaxHeight(height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(cFaint).
		PaddingLeft(1).
		Render(fitWidth(strings.Join(s, "\n"), w))
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

func usageLine(name string, win *claude.Window, w int) []string {
	pct := fmt.Sprintf(" %3.0f%%", win.Utilization*100)
	lines := []string{sDim.Render(name+" ") + bar(win.Utilization, w-3-len(pct)) + sText.Render(pct)}
	if win.ResetsAt > 0 {
		lines = append(lines, sFaint.Render("   resets "+time.Unix(win.ResetsAt, 0).Format("Mon 15:04")))
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
// header, the tab strip, the status bar and a line of transcript.
func (m *Model) bottomRoom() int {
	return max(m.h-3-m.tabsHeight(), 1)
}

// queueView lists messages waiting for the current turn, faint and just
// above the input they came from. It takes no space when the queue is empty.
func (m *Model) queueView() string {
	var out []string
	for i, q := range m.queue {
		if i == 2 && len(m.queue) > 3 {
			out = append(out, sFaint.Render(fmt.Sprintf(" … %d more queued", len(m.queue)-2)))
			break
		}
		q = strings.Join(strings.Fields(q), " ")
		out = append(out, sFaint.Render(" › "+truncate(q, m.w-32)))
	}
	if len(out) > 0 {
		out[0] += sFaint.Render("  click or ctrl+e to edit")
	}
	return strings.Join(out, "\n")
}

// inputView draws the input box with the permission mode set into its bottom
// border, so the mode sits right where you type without crowding the text.
func (m *Model) inputView() string {
	col := modeColor(m.mode)
	bc := lipgloss.NewStyle().Foreground(cFaint)
	label := " " + lipgloss.NewStyle().Foreground(col).Render("● "+modeLabel(m.mode)) + " "
	right := sFaint.Render(" shift+tab ")
	w := m.w - 2
	// The body is w wide plus its two side borders; the bottom line must
	// match: "╰─" + label + fill + right + "─╯".
	fill := w + 2 - 4 - lipgloss.Width(label) - lipgloss.Width(right)
	top := bc.Render("╭" + strings.Repeat("─", w) + "╮")
	bottom := bc.Render("╰─") + label + bc.Render(strings.Repeat("─", max(fill, 0))) + right + bc.Render("─╯")
	body := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, false, true).
		BorderForeground(cFaint).
		Padding(0, 1).
		Width(w).
		// The textarea measures Thai and Lao SARA AM a cell short, so a line of
		// them can draw past the right border and wrap, which pushes the box
		// taller than the space layout gave it and shoves the hints and status
		// bar off the bottom of the screen.
		Render(fitWidth(m.input.View(), max(w-2, 1)))
	return top + "\n" + body + "\n" + bottom
}

func (m *Model) permView() string {
	p := m.perms[0]
	var in map[string]any
	json.Unmarshal(p.Input, &in)
	inner := m.w - 6

	title := sYellow.Bold(true).Render("⚠  Approval needed") + "   " + sBold.Render(displayName(p.ToolName))
	if p.Description != "" {
		room := inner - lipgloss.Width(title) - 3
		if room > 8 {
			title += "  " + sDim.Render(truncate(p.Description, room))
		}
	}
	// Leave the prompt's frame, title and keys (6 lines), the header and
	// status bar, and a few transcript lines on screen.
	budget := max(m.h-14, 2)
	var body []string
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
	opts := []string{pill(" y ", cGreen) + sText.Render(" allow once")}
	if hasSuggestions(p) {
		always := "always allow"
		if p.AlwaysLabel != "" {
			always = p.AlwaysLabel
		}
		opts = append(opts, pill(" a ", cAccent2)+sText.Render(" "+termSafe(always)))
	}
	opts = append(opts, pill(" n ", cRed)+sText.Render(" deny"))
	keys := strings.Join(opts, "    ")
	if len(m.perms) > 1 {
		keys += sFaint.Render(fmt.Sprintf("    +%d more waiting", len(m.perms)-1))
	}
	content := strings.Join(append(append([]string{title, ""}, body...), "", keys), "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cYellow).
		Padding(0, 1).
		Width(m.w - 2).
		Render(content)
}

// statusBar: what is happening on the left, what you can do right now on
// the right. Hints change with context so there are never more than a few.
func (m *Model) statusBar() string {
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
		right = hints("esc esc", "interrupt", "ctrl+o", "details")
	case m.engine == "":
		left = sFaint.Render(" ○") + sDim.Render(" not connected")
		right = hints("/login", "connect", "ctrl+k", "palette")
	default:
		left = sGreen.Render(" ●") + sDim.Render(" ready")
		if sp := m.spendLine(); m.sideW == 0 && sp != "" {
			// The sidebar isn't showing what this has cost, so say it here.
			left += sFaint.Render("  " + sp)
		}
		right = hints("enter", "send", "shift+enter", "newline", "ctrl+k", "palette")
	}
	if m.notice != "" && time.Since(m.noticeAt) < 4*time.Second {
		st := sYellow
		if m.noticeOK {
			st = sGreen
		}
		left += "   " + st.Render(m.notice)
	}
	right += " "
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Narrow window: keep what's happening, drop the hints.
		return truncate(left, m.w)
	}
	// Clamp: lipgloss under-measures Thai and Lao SARA AM by a cell each, so a
	// notice full of them can still come out wider than the window and wrap,
	// pushing the input box and the hints off the bottom of the screen.
	return truncate(left+strings.Repeat(" ", gap)+right, m.w)
}

// spendLine is this session's use, and every session's when there are several.
func (m *Model) spendLine() string {
	here := spendLabel(m.cost, m.tokIn, m.tokOut)
	if all := m.totalSpend(); len(m.sessions) > 1 && all != "" && all != here {
		return strings.TrimSpace(here + " · " + all + " in all")
	}
	return here
}

// shimmer paints text with a bright band sweeping across it.
func shimmer(s string, frame int) string {
	runes := []rune(s)
	pos := frame%(len(runes)+10) - 3
	var sb strings.Builder
	for i, r := range runes {
		d := i - pos
		if d < 0 {
			d = -d
		}
		t := 0.0
		if d < 3 {
			t = 0.6 - float64(d)*0.2
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(mix(cAccent, lipgloss.Color("#FFFFFF"), t)).Render(string(r)))
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
