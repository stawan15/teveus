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
// bottom unless the user has scrolled up.
func (m *Model) refresh() {
	if m.w == 0 {
		return
	}
	follow := m.vp.AtBottom() || m.vp.TotalLineCount() <= m.vp.Height
	width := min(m.chatW-3, 120) // long lines are hard to read; cap the measure
	var sb strings.Builder
	line := 0
	write := func(s string) {
		sb.WriteString(s)
		line += strings.Count(s, "\n")
	}
	if len(m.blocks) == 0 {
		write(m.welcome(m.chatW - 3))
	}
	m.blockStart = m.blockStart[:0]
	for i, b := range m.blocks {
		if i > 0 {
			// Proximity: consecutive tool calls sit together, turns breathe.
			prev := m.blocks[i-1]
			// Pro is compact: blank lines only between turns.
			switch {
			case b.kind == kindUser:
				// A faint rule opens each turn, so replies don't run together.
				write("\n\n" + sFaint.Render(strings.Repeat("─", width)) + "\n\n")
			case (prev.kind == kindTool && b.kind == kindTool) || m.level() == "pro":
				write("\n")
			default:
				write("\n\n")
			}
		}
		m.blockStart = append(m.blockStart, line)
		write(m.r.render(b, width))
	}
	write("\n")
	// A one-cell margin, added by hand: lipgloss would measure and pad every
	// line of the whole transcript on each streamed chunk.
	m.lines = strings.Split(sb.String(), "\n")
	for i, l := range m.lines {
		m.lines[i] = " " + strings.ReplaceAll(l, "\t", "    ") // terminals' tab stops would skew widths
	}
	m.vp.SetLines(m.lines)
	if follow {
		m.vp.GotoBottom()
	}
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
		main = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(m.chatW).Render(main), m.sidebar(m.vp.Height))
	}
	rows := []string{m.header()}
	if t := m.tabStrip(); t != "" {
		rows = append(rows, t)
	}
	return lipgloss.JoinVertical(lipgloss.Left, append(rows, main, m.bottomFitted(), m.statusBar())...)
}

// bottomFitted is the bottom area cut to leave room for the header, the
// status bar and a line of transcript. A frame taller than the window makes
// the terminal scroll, and every later frame lands in the wrong place.
func (m *Model) bottomFitted() string {
	b := m.bottomView()
	room := max(m.h-3-m.tabsHeight(), 1)
	if lines := strings.Split(b, "\n"); len(lines) > room {
		b = strings.Join(lines[len(lines)-room:], "\n") // keep the end: the keys to answer
	}
	return b
}

func (m *Model) header() string {
	left := gradient(" ✦ teveus", cAccent, cAccent2, true)
	if m.engine == "api" {
		left += " " + pill("API", cAccent2)
	}
	sep := sFaint.Render("  ·  ")
	parts := []string{sDim.Render(shortPath(m.cwd))}
	if m.branch != "" {
		parts = append(parts, sAccent2.Render("⎇ "+m.branch))
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

// starters are example first prompts: new users see what the tool can do and
// can start with one keypress (1-4).
var starters = []string{
	"Explain what this project does and how it's organised",
	"Find a bug in this code and fix it",
	"Write tests for the most important function",
	"Review my uncommitted changes",
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
		for _, l := range logo {
			lines = append(lines, gradient(l, cAccent, cAccent2, true))
		}
		lines = append(lines, "")
	}
	greeting := "What should we build today?"
	if m.engine == "api" && m.model != "" {
		greeting += sFaint.Render("  ·  " + shortModel(m.model))
	}
	lines = append(lines, sDim.Render(greeting), "")
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
	for i, s := range starters {
		rows = append(rows, keycap(fmt.Sprint(i+1))+" "+sText.Render(s))
	}
	lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, rows...), "")

	tips := [][2]string{
		{"?", "all shortcuts"}, {"ctrl+k", "command palette"},
		{"@", "mention a file"}, {"/", "slash commands"},
		{"shift+tab", "permission mode"}, {"/settings", "preferences"},
	}
	colW := 0
	for _, t := range tips {
		colW = max(colW, lipgloss.Width(keycap(t[0]))+1+lipgloss.Width(t[1]))
	}
	cell := func(t [2]string) string {
		s := keycap(t[0]) + " " + sDim.Render(t[1])
		return s + strings.Repeat(" ", colW-lipgloss.Width(s))
	}
	twoCols := width >= colW*2+8
	for i := 0; i < len(tips); i += 2 {
		if twoCols {
			lines = append(lines, cell(tips[i])+"    "+cell(tips[i+1]))
		} else {
			lines = append(lines, cell(tips[i]), cell(tips[i+1]))
		}
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
		t := sAccent2.Bold(true).Render(title)
		if right != "" {
			t += " " + sDim.Render(right)
		}
		rule := w - lipgloss.Width(t) - 1
		s = append(s, t+" "+sFaint.Render(strings.Repeat("─", max(rule, 0))))
	}
	kv := func(k, v string) string {
		return sDim.Render(fmt.Sprintf("%-9s", k)) + v
	}

	section("SESSION", "")
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
		s = append(s, kv("effort", sAccent2.Render(m.settings.Effort)))
	}
	s = append(s, kv("saver", sGreen.Render(m.saverLabel())))
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

	if m.fiveHour != nil || m.sevenDay != nil {
		section("USAGE", "")
		if m.fiveHour != nil {
			s = append(s, usageLine("5h", m.fiveHour, w)...)
		}
		if m.sevenDay != nil {
			s = append(s, usageLine("7d", m.sevenDay, w)...)
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

	return lipgloss.NewStyle().
		Width(m.sideW-1).Height(height).MaxHeight(height).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(cFaint).
		PaddingLeft(1).
		Render(strings.Join(s, "\n"))
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
	var parts []string
	if m.pop.open() {
		parts = append(parts, m.popupView())
	}
	if len(m.perms) > 0 {
		parts = append(parts, m.permView())
	} else {
		parts = append(parts, m.inputView())
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// inputView draws the input box with the permission mode set into its top
// border, so the mode sits right where you type.
func (m *Model) inputView() string {
	col := modeColor(m.mode)
	bc := lipgloss.NewStyle().Foreground(col)
	w := m.w - 2
	label := lipgloss.NewStyle().Foreground(col).Bold(true).Render(" ● " + modeLabel(m.mode) + " ")
	right := sFaint.Render(" shift+tab ")
	// The body is w wide plus its two side borders; the top line must match:
	// "╭─" + label + fill + right + "─╮".
	fill := w + 2 - 4 - lipgloss.Width(label) - lipgloss.Width(right)
	top := bc.Render("╭─") + label + bc.Render(strings.Repeat("─", max(fill, 0))) + right + bc.Render("─╮")
	body := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(col).
		Padding(0, 1).
		Width(w).
		Render(m.input.View())
	return top + "\n" + body
}

func (m *Model) permView() string {
	p := m.perms[0]
	var in map[string]any
	json.Unmarshal(p.Input, &in)
	inner := m.w - 6

	title := sYellow.Bold(true).Render("⚠ Allow " + displayName(p.ToolName) + "?")
	if p.Description != "" {
		title += "  " + sDim.Render(truncate(p.Description, inner-20))
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
	return left + strings.Repeat(" ", gap) + right
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
