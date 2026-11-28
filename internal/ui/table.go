package ui

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type mdTable struct {
	token  string
	output string
}

var tableSepCellRe = regexp.MustCompile(`^:?-{1,}:?$`)

// extractMarkdownTables replaces GFM pipe tables outside fenced code blocks
// with placeholder tokens and renders each table as a compact rounded box.
func extractMarkdownTables(src string, width int) (string, []mdTable) {
	if !strings.Contains(src, "|") {
		return src, nil
	}
	lines := strings.Split(src, "\n")
	var out []string
	var tables []mdTable
	inFence := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, lines[i])
			continue
		}
		if !inFence && i+1 < len(lines) && looksLikeTableRow(lines[i]) {
			if aligns, ok := parseTableSeparator(lines[i+1]); ok {
				headers := splitTableRow(lines[i])
				if len(headers) == len(aligns) && len(headers) > 0 {
					var rows [][]string
					j := i + 2
					for j < len(lines) && looksLikeTableRow(lines[j]) {
						row := splitTableRow(lines[j])
						for len(row) < len(aligns) {
							row = append(row, "")
						}
						if len(row) > len(aligns) {
							row = row[:len(aligns)]
						}
						rows = append(rows, row)
						j++
					}
					tok := fmt.Sprintf("TEVEUSTABLE%dEND", len(tables))
					tables = append(tables, mdTable{
						token:  tok,
						output: renderBoxTable(headers, aligns, rows, width),
					})
					out = append(out, tok)
					i = j - 1
					continue
				}
			}
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n"), tables
}

func restoreMarkdownTables(rendered string, tables []mdTable) string {
	for _, tb := range tables {
		rendered = strings.Replace(rendered, tb.token, tb.output, 1)
	}
	return rendered
}

func looksLikeTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.Contains(t, "|") && len(t) > 1
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseTableSeparator(line string) ([]int, bool) {
	if !looksLikeTableRow(line) {
		return nil, false
	}
	cells := splitTableRow(line)
	if len(cells) == 0 {
		return nil, false
	}
	aligns := make([]int, len(cells))
	for i, c := range cells {
		if !tableSepCellRe.MatchString(c) {
			return nil, false
		}
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns[i] = 1 // center
		case right:
			aligns[i] = 2 // right
		default:
			aligns[i] = 0 // left
		}
	}
	return aligns, true
}

func renderBoxTable(headers []string, aligns []int, rows [][]string, maxWidth int) string {
	cols := len(headers)
	styledHead := make([]string, cols)
	colW := make([]int, cols)
	for c := 0; c < cols; c++ {
		styledHead[c] = styleTableCell(headers[c], true)
		colW[c] = max(ansi.StringWidth(styledHead[c]), 1)
	}
	styledRows := make([][]string, len(rows))
	for r, row := range rows {
		styledRows[r] = make([]string, cols)
		for c := 0; c < cols; c++ {
			styledRows[r][c] = styleTableCell(row[c], false)
			if w := ansi.StringWidth(styledRows[r][c]); w > colW[c] {
				colW[c] = w
			}
		}
	}

	// Budget: "│ " + cell + " │" => 3*cols + 1 border/padding cells.
	avail := max(maxWidth-(3*cols+1), cols*4)
	total := 0
	for _, w := range colW {
		total += w
	}
	for total > avail {
		best := -1
		for c, w := range colW {
			if w > 4 && (best < 0 || w > colW[best]) {
				best = c
			}
		}
		if best < 0 {
			break
		}
		colW[best]--
		total--
	}

	borderLine := func(left, mid, right string) string {
		parts := make([]string, cols)
		for c, w := range colW {
			parts[c] = strings.Repeat("─", w+2)
		}
		return sBorder.Render(left + strings.Join(parts, mid) + right)
	}

	renderRow := func(cells []string) []string {
		wrapped := make([][]string, cols)
		height := 1
		for c, cell := range cells {
			w := colW[c]
			lines := strings.Split(ansi.Wordwrap(cell, w, ""), "\n")
			var split []string
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					split = append(split, strings.Split(ansi.Hardwrap(l, w, false), "\n")...)
				} else {
					split = append(split, l)
				}
			}
			if len(split) == 0 {
				split = []string{""}
			}
			wrapped[c] = split
			if len(split) > height {
				height = len(split)
			}
		}
		out := make([]string, height)
		vbar := sBorder.Render("│")
		for lineIdx := 0; lineIdx < height; lineIdx++ {
			var b strings.Builder
			b.WriteString(vbar)
			for c := 0; c < cols; c++ {
				cellLine := ""
				if lineIdx < len(wrapped[c]) {
					cellLine = wrapped[c][lineIdx]
				}
				cw := ansi.StringWidth(cellLine)
				pad := max(colW[c]-cw, 0)
				leftPad, rightPad := 0, pad
				switch aligns[c] {
				case 1:
					leftPad = pad / 2
					rightPad = pad - leftPad
				case 2:
					leftPad = pad
					rightPad = 0
				}
				b.WriteByte(' ')
				if leftPad > 0 {
					b.WriteString(strings.Repeat(" ", leftPad))
				}
				b.WriteString(cellLine)
				if rightPad > 0 {
					b.WriteString(strings.Repeat(" ", rightPad))
				}
				b.WriteByte(' ')
				b.WriteString(vbar)
			}
			out[lineIdx] = b.String()
		}
		return out
	}

	var lines []string
	lines = append(lines, borderLine("╭", "┬", "╮"))
	lines = append(lines, renderRow(styledHead)...)
	lines = append(lines, borderLine("├", "┼", "┤"))
	for _, row := range styledRows {
		lines = append(lines, renderRow(row)...)
	}
	lines = append(lines, borderLine("╰", "┴", "╯"))
	return strings.Join(lines, "\n")
}

var (
	cellCodeRe = regexp.MustCompile("`([^`]+)`")
	cellBoldRe = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

func styleTableCell(s string, header bool) string {
	s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdLinkRe.FindStringSubmatch(m)
		if len(sub) != 3 || sub[1] == "" {
			return m
		}
		h := fnv.New32a()
		h.Write([]byte(sub[2]))
		return fmt.Sprintf("\x1b]8;id=%x;%s\x1b\\\x1b[4m%s\x1b[24m\x1b]8;;\x1b\\", h.Sum32(), sub[2], sub[1])
	})
	s = cellCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cellCodeRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return sAccent.Render(sub[1])
	})
	s = cellBoldRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := cellBoldRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		return sBold.Render(sub[1])
	})
	if header {
		return sBold.Render(s)
	}
	return s
}
