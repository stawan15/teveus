package ui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type popupMode int

const (
	popNone    popupMode = iota
	popSlash             // filtered by the input box ("/…")
	popMention           // filtered by the input box ("@…")
	popPalette           // ctrl+k: has its own search field
	popPicker            // a titled choice list (model, effort, theme…)
	popInput             // a one-line text field (API keys, URLs)
	popSlider            // ordered levels on a track (slider.go)
)

type choice struct {
	label   string
	desc    string
	key     string   // shortcut or metadata shown on the right
	badge   string   // short highlighted tag, e.g. "free"
	aliases []string // short names that match exactly, e.g. "q" for /exit
	group   string
	value   string
	run     func(m *Model) tea.Cmd
}

type popup struct {
	mode    popupMode
	title   string
	query   string
	all     []choice
	list    []choice
	sel     int
	flat    bool                     // don't group rows under headers
	ends    [2]string                // popSlider: what the low and high ends mean
	colors  []lipgloss.Color         // popSlider: one per level (default: the theme's spectrum)
	body    []string                 // popPicker: styled lines shown under the title
	hold    string                   // popPicker: value of a choice that can't be picked yet…
	holdEnd time.Time                // …until this time, so the body gets read
	preview func(m *Model, c choice) // called as the selection moves
	cancel  func(m *Model)           // called on esc

	// popInput
	hint   string
	masked bool
	submit func(m *Model, v string) tea.Cmd
}

func (p *popup) open() bool { return p.mode != popNone }

// held reports whether c can't be picked yet (see popup.hold).
func (p *popup) held(c choice) bool {
	return p.hold != "" && c.value == p.hold && time.Now().Before(p.holdEnd)
}

func (p *popup) close() { *p = popup{} }

func (p *popup) filter() {
	p.list = fuzzyFilter(p.all, p.query, p.mode == popMention)
	if p.sel >= len(p.list) {
		p.sel = max(len(p.list)-1, 0)
	}
}

func (p *popup) move(d int) {
	if len(p.list) > 0 {
		p.sel = (p.sel + d + len(p.list)) % len(p.list)
	}
}

func (p *popup) current() (choice, bool) {
	if p.sel < len(p.list) {
		return p.list[p.sel], true
	}
	return choice{}, false
}

// fuzzyFilter ranks by: exact prefix, then word-start match, then substring,
// then subsequence. For paths the file name counts more than the directory.
func fuzzyFilter(items []choice, q string, paths bool) []choice {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return items
	}
	type scored struct {
		c     choice
		score int
	}
	var out []scored
	for _, it := range items {
		label := strings.ToLower(it.label)
		extra := strings.ToLower(it.desc + " " + it.group + " " + it.key + " " + it.badge)
		total, ok := 0, true
		// Every word must match: the label scores best, then the full name,
		// group, price or badge ("claude fable", "free", "openai mini").
		for _, w := range words {
			s, hit := fuzzyScore(w, label)
			for _, a := range it.aliases {
				if a == w {
					s, hit = 2000, true // "/q" → /exit first
				} else if strings.HasPrefix(a, w) && s < 1200 {
					s, hit = 1200, true
				}
			}
			if paths {
				if bs, ok := fuzzyScore(w, strings.ToLower(filepath.Base(it.label))); ok {
					s, hit = max(s, bs+50), true
				}
			}
			if !hit {
				if strings.Contains(extra, w) {
					s, hit = 300-strings.Index(extra, w)/4, true
				}
			}
			if !hit {
				ok = false
				break
			}
			total += s
		}
		if !ok {
			continue
		}
		if paths {
			total -= strings.Count(it.label, "/") * 2
		}
		out = append(out, scored{it, total})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]choice, 0, len(out))
	seen := map[string]bool{}
	for _, o := range out {
		if o.c.value != "" && (o.c.group == "Recent" || o.c.group == "Free") {
			continue // the same model is also listed under its maker
		}
		if o.c.value != "" && seen[o.c.value] {
			continue
		}
		seen[o.c.value] = true
		res = append(res, o.c)
	}
	return res
}

func fuzzyScore(q, t string) (int, bool) {
	t = strings.TrimPrefix(t, "/")
	switch {
	case strings.HasPrefix(t, q):
		return 1000 - len(t), true
	case strings.Contains(t, q):
		i := strings.Index(t, q)
		bonus := 0
		if i > 0 && !unicode.IsLetter(rune(t[i-1])) {
			bonus = 200 // starts a word: "rev" in "code-review"
		}
		return 500 + bonus - i - len(t), true
	}
	// subsequence, rewarding consecutive runs
	score, ti, run := 0, 0, 0
	for _, qc := range q {
		found := false
		for ti < len(t) {
			tc := rune(t[ti])
			ti++
			if tc == qc {
				run++
				score += 5 * run
				found = true
				break
			}
			run = 0
		}
		if !found {
			return 0, false
		}
	}
	return score - len(t), true
}

// enumArg extracts choices from argument hints like "<low|medium|high>".
var enumArg = regexp.MustCompile(`^[<\[]\s*([\w.-]+(?:\s*\|\s*[\w.-]+)+)\s*[>\]]$`)

func parseEnum(hint string) []string {
	m := enumArg.FindStringSubmatch(strings.TrimSpace(hint))
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range strings.Split(m[1], "|") {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func (m *Model) popupView() string {
	p := &m.pop
	width := m.w - 2
	inner := width - 4
	if p.mode == popSlider {
		return m.sliderView()
	}
	if p.mode == popInput {
		v := p.query
		if p.masked && v != "" {
			v = strings.Repeat("•", min(len([]rune(v)), inner-8))
		}
		lines := []string{sAccent.Bold(true).Render(p.title)}
		if p.hint != "" {
			lines = append(lines, sDim.Width(inner).Render(p.hint))
		}
		lines = append(lines, "", sAccent.Render("› ")+sText.Render(v)+sAccent.Render("▏"),
			"", sFaint.Render("enter confirm · esc cancel · ctrl+u clear"))
		return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
			Padding(0, 1).Width(width).Render(strings.Join(lines, "\n"))
	}

	rows := 8
	if p.mode == popPicker || p.mode == popPalette {
		rows = max(8, min(18, (m.h-12)/2)) // long lists get room to breathe
	}

	var lines []string
	if p.title != "" || p.mode == popPalette || p.mode == popPicker {
		title := p.title
		if title == "" {
			title = "Command palette"
		}
		head := sAccent.Bold(true).Render(title)
		if len(p.all) > 12 {
			unique := map[string]bool{}
			for _, c := range p.all {
				unique[c.value+"\x00"+c.label] = true
				if c.value != "" {
					unique[c.value+"\x00"+c.label] = false
					unique[c.value] = true
				}
			}
			n := 0
			for _, v := range unique {
				if v {
					n++
				}
			}
			head += sDim.Render(fmt.Sprintf("  %d", n))
			if p.query != "" {
				head += sDim.Render(fmt.Sprintf(" · %d match", len(p.list)))
			}
		}
		if p.mode == popPalette || (p.mode == popPicker && (len(p.all) > 8 || p.query != "")) {
			q := sFaint.Render("type to filter…")
			if p.query != "" {
				q = sText.Render(p.query)
			}
			head += "   " + sAccent.Render("⌕ ") + q + sAccent.Render("▏")
		}
		lines = append(lines, head)
		if p.hint != "" && p.mode == popPicker {
			lines = append(lines, sDim.Width(inner).Render(p.hint))
		}
		for _, b := range p.body {
			lines = append(lines, lipgloss.NewStyle().Width(inner).Render(b))
		}
		lines = append(lines, "")
	}

	if len(p.list) == 0 {
		lines = append(lines, sDim.Render("  no matches"))
	}

	// Column widths come from the whole list so they don't jump while scrolling.
	labelW, metaW := 0, 0
	groups := map[string]int{}
	for _, c := range p.list {
		labelW = max(labelW, lipgloss.Width(c.label))
		metaW = max(metaW, lipgloss.Width(c.key))
		groups[c.group]++
	}
	labelW = min(labelW, inner*2/5)
	metaW = min(metaW, inner/4)
	grouped := p.query == "" && len(groups) > 1 && !p.flat

	// Scroll so the selection stays in view, counting group header lines.
	start := 0
	if p.sel >= rows {
		start = p.sel - rows + 1
	}
	end := min(start+rows, len(p.list))
	lastGroup := ""
	if start > 0 {
		lastGroup = p.list[start-1].group
	}
	for i := start; i < end; i++ {
		c := p.list[i]
		held := p.held(c)
		if held {
			c.desc = fmt.Sprintf("take a moment to read the above · %ds", int(time.Until(p.holdEnd).Seconds())+1)
		}
		if grouped && c.group != lastGroup {
			if i > start {
				lines = append(lines, "")
			}
			header := sAccent2.Bold(true).Render(strings.ToUpper(c.group))
			if len(p.all) > 20 {
				header += sFaint.Render(fmt.Sprintf("  %d", groups[c.group]))
			}
			lines = append(lines, header)
		}
		lastGroup = c.group
		sel := i == p.sel
		st := func(x lipgloss.Style) lipgloss.Style {
			if sel {
				return x.Background(cSurface)
			}
			return x
		}

		marker, lab, dsc := st(sText).Render("  "), st(sText), st(sDim)
		if sel {
			marker, lab, dsc = st(sAccent).Render("› "), st(sAccent).Bold(true), st(sText)
		}
		if held {
			lab, dsc = st(sFaint), st(sFaint)
		}
		label := truncate(c.label, labelW)
		label += strings.Repeat(" ", labelW-lipgloss.Width(label))

		right := ""
		meta := c.key
		if !grouped && c.group != "" && p.mode == popPicker && len(groups) > 1 {
			meta = strings.TrimSpace(meta + "  " + c.group)
		}
		if c.badge != "" {
			right += st(lipgloss.NewStyle().Foreground(cGreen).Bold(true)).Render(c.badge)
			if meta != "" {
				right += st(sText).Render("  ")
			}
		}
		if meta != "" {
			right += st(sDim).Render(truncate(meta, max(metaW, 12)+20))
		}
		descW := max(inner-2-labelW-2-lipgloss.Width(right)-2, 0)
		desc := truncate(c.desc, max(descW, 1))
		if descW == 0 {
			desc = ""
		}
		row := marker + lab.Render(label) + st(sText).Render("  ") + dsc.Render(desc)
		gap := inner - lipgloss.Width(row) - lipgloss.Width(right)
		row += st(sText).Render(strings.Repeat(" ", max(gap, 1))) + right
		lines = append(lines, row)
	}
	if len(p.list) > rows {
		lines = append(lines, "", sFaint.Render(fmt.Sprintf("  %d/%d", p.sel+1, len(p.list)))+
			sFaint.Render("   ↑↓ scroll · type to filter"))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cFaint).
		Padding(0, 1).
		Width(width).
		Render(strings.Join(lines, "\n"))
}
