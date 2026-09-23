package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A slider picks one of a few ordered levels (guidance, minimal code,
// reasoning effort): the choices sit on a track and ← → moves along it.
//
//	off ●━━━━━━━━━━●━━━━━━━━━━○──────────○ strict

// neutralFirst colours a scale whose first level means "not set" (auto,
// off) in grey, and the rest along the spectrum.
func neutralFirst(n int) []lipgloss.Color {
	return append([]lipgloss.Color{cDim}, spectrum(n-1)...)
}

// openSlider shows cs as ordered levels; each choice's run applies it.
func (m *Model) openSlider(title, hint string, cs []choice, sel int) {
	m.pop = popup{mode: popSlider, title: title, hint: hint, all: cs, list: cs, flat: true}
	m.pop.sel = max(0, min(sel, len(cs)-1))
	m.layout()
}

func (m *Model) handleSliderKey(k tea.KeyMsg) tea.Cmd {
	p := &m.pop
	switch k.String() {
	case "left", "h", "down", "shift+tab":
		p.sel = max(p.sel-1, 0)
	case "right", "l", "up", "tab":
		p.sel = min(p.sel+1, len(p.list)-1)
	case "home":
		p.sel = 0
	case "end":
		p.sel = len(p.list) - 1
	case "esc", "ctrl+c", "q":
		m.closePopup(true)
	case "enter", " ":
		c, ok := p.current()
		m.closePopup(false)
		if ok && c.run != nil {
			return c.run(m)
		}
	default:
		// 1-9 jumps straight to a level.
		if r := []rune(k.String()); len(r) == 1 && r[0] >= '1' && r[0] <= '9' && int(r[0]-'1') < len(p.list) {
			p.sel = int(r[0] - '1')
		}
	}
	return nil
}

func (m *Model) sliderView() string {
	p := &m.pop
	width := m.w - 2
	inner := width - 4
	n := len(p.list)
	colors := p.colors
	if len(colors) != n {
		colors = spectrum(n)
	}
	cur := colors[p.sel]
	lines := []string{gradient(p.title, cAccent, cAccent2, true)}
	if p.hint != "" {
		lines = append(lines, sDim.Render(truncate(p.hint, inner)))
	}
	lines = append(lines, "")

	// Space the points evenly, leaving room to centre the end labels.
	labelW := 0
	for _, c := range p.list {
		labelW = max(labelW, ansi.StringWidth(c.label)+2)
	}
	track := min(inner-labelW-2, max(12, 14*(n-1)))
	if track < 2*(n-1)+1 {
		track = 2*(n-1) + 1
	}
	pos := make([]int, n)
	for i := range pos {
		if n > 1 {
			pos[i] = i * (track - 1) / (n - 1)
		}
	}
	margin := labelW / 2

	// The filled part of the track blends from each level's colour to the
	// next; the rest stays faint.
	var bar strings.Builder
	bar.WriteString(strings.Repeat(" ", margin))
	seg := 0
	for x := 0; x < track; x++ {
		for seg < n-1 && x >= pos[seg+1] {
			seg++
		}
		point := -1
		for i, px := range pos {
			if px == x {
				point = i
			}
		}
		switch {
		case point == p.sel:
			bar.WriteString(lipgloss.NewStyle().Foreground(cur).Bold(true).Render("◆"))
		case point >= 0 && point < p.sel:
			bar.WriteString(lipgloss.NewStyle().Foreground(colors[point]).Render("●"))
		case point >= 0:
			bar.WriteString(sFaint.Render("○"))
		case x < pos[p.sel]:
			t := float64(x-pos[seg]) / float64(max(pos[seg+1]-pos[seg], 1))
			bar.WriteString(lipgloss.NewStyle().Foreground(mix(colors[seg], colors[seg+1], t)).Render("━"))
		default:
			bar.WriteString(sFaint.Render("─"))
		}
	}
	lines = append(lines, bar.String())

	// Labels centred under their points: the chosen one as a pill, the ones
	// already passed in their colour, the rest dim.
	var lab strings.Builder
	col := 0
	for i, c := range p.list {
		var styled string
		w := ansi.StringWidth(c.label)
		switch {
		case i == p.sel:
			styled, w = pill(c.label, cur), w+2
		case i < p.sel:
			styled = lipgloss.NewStyle().Foreground(colors[i]).Render(c.label)
		default:
			styled = sDim.Render(c.label)
		}
		start := max(col, margin+pos[i]-w/2)
		if i > 0 && start == col {
			start++ // never let labels touch
		}
		lab.WriteString(strings.Repeat(" ", start-col))
		lab.WriteString(styled)
		col = start + w
	}
	lines = append(lines, truncate(lab.String(), inner))

	if p.ends[0] != "" || p.ends[1] != "" {
		// What moving left or right trades, in the colours of the two ends.
		lw, rw := ansi.StringWidth(p.ends[0]), ansi.StringWidth(p.ends[1])
		if gap := margin + track - lw - rw - 4; gap >= 1 {
			lo := lipgloss.NewStyle().Foreground(colors[min(1, n-1)])
			hi := lipgloss.NewStyle().Foreground(colors[n-1])
			lines = append(lines, lo.Render("◂ "+p.ends[0])+strings.Repeat(" ", gap)+hi.Render(p.ends[1]+" ▸"))
		}
	}

	if c, ok := p.current(); ok && c.desc != "" {
		dot := lipgloss.NewStyle().Foreground(cur).Render("● ")
		lines = append(lines, "", dot+sText.Width(inner-2).Render(termSafe(c.desc)))
	}
	// The frame takes the chosen level's colour, so the whole box answers
	// each key press.
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cur).
		Padding(0, 1).Width(width).Render(strings.Join(lines, "\n"))
}

// spectrum gives n level colours from the theme, cool to hot:
// blue → green → yellow → accent → red.
func spectrum(n int) []lipgloss.Color {
	stops := []lipgloss.Color{cBlue, cGreen, cYellow, cAccent, cRed}
	out := make([]lipgloss.Color, n)
	for i := range out {
		if n < 2 {
			out[i] = cAccent
			continue
		}
		t := float64(i) / float64(n-1)
		f := t * float64(len(stops)-1)
		k := min(int(f), len(stops)-2)
		out[i] = mix(stops[k], stops[k+1], f-float64(k))
	}
	return out
}

// ordinalWords are option names that form a scale, so a command offering
// only these (like /effort low|medium|high) gets a slider.
var ordinalWords = map[string]int{
	"off": 0, "none": 0, "minimal": 1, "min": 1, "lite": 1, "low": 2, "medium": 3, "med": 3, "default": 3,
	"full": 4, "high": 4, "xhigh": 5, "strict": 5, "max": 6, "ultra": 6,
}

func isScale(opts []string) bool {
	if len(opts) < 3 {
		return false
	}
	prev := -1
	for _, o := range opts {
		v, ok := ordinalWords[strings.ToLower(o)]
		if !ok || v <= prev {
			return false
		}
		prev = v
	}
	return true
}
