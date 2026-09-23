package ui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type blockKind int

const (
	kindUser blockKind = iota
	kindAssistant
	kindTool
	kindInfo
	kindError
	kindTurnEnd
	kindCmdOut // output of a slash command that didn't involve the model
)

type toolState int

const (
	toolRunning toolState = iota
	toolDone
	toolFailed
)

// block is one entry of the transcript. Rendered output is cached per width
// so only changed blocks are re-rendered.
type block struct {
	kind      blockKind
	text      string
	streaming bool

	// tools
	id       string
	name     string
	input    map[string]any
	result   string
	state    toolState
	nested   bool
	open     bool // expanded by clicking
	started  time.Time
	finished time.Time

	cache  string
	cacheW int
	dirty  bool
}

func (b *block) invalidate() { b.dirty = true }

type renderer struct {
	cwd    string
	md     *glamour.TermRenderer
	mdW    int
	expand bool
	spin   string
}

func (r *renderer) markdown(s string, width int) string {
	if r.md == nil || r.mdW != width {
		style := "dark"
		if !theme.Dark {
			style = "light"
		}
		md, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(style),
			glamour.WithWordWrap(width-2),
			glamour.WithEmoji(),
		)
		if err != nil {
			return s
		}
		r.md, r.mdW = md, width
	}
	out, err := r.md.Render(s)
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}

func (r *renderer) render(b *block, width int) string {
	if !b.dirty && b.cacheW == width && b.cache != "" {
		return b.cache
	}
	var out string
	switch b.kind {
	case kindUser:
		out = sUser.Width(width - 2).Render(b.text)
	case kindAssistant:
		if b.streaming {
			// Raw text while streaming; markdown once the block is complete.
			out = lipgloss.NewStyle().Width(width-2).PaddingLeft(2).Foreground(cText).Render(b.text) +
				sAccent.Render(" ▍")
		} else {
			out = r.markdown(b.text, width)
		}
	case kindTool:
		out = r.tool(b, width)
	case kindInfo:
		out = sDim.Italic(true).Width(width - 2).PaddingLeft(2).Render(b.text)
	case kindError:
		out = sRed.Width(width - 2).PaddingLeft(2).Render("✗ " + b.text)
	case kindTurnEnd:
		out = "  " + sGreen.Render("✓ ") + sDim.Render(b.text)
	case kindCmdOut:
		// Command output is often Markdown (tables, lists): render it, set
		// off by a gutter so it reads as output rather than a reply.
		lines := strings.Split(r.markdown(strings.TrimSpace(b.text), width-4), "\n")
		for i := range lines {
			lines[i] = "  " + sFaint.Render("│") + lines[i]
		}
		out = strings.Join(lines, "\n")
	}
	b.cache, b.cacheW, b.dirty = out, width, false
	return out
}

func (r *renderer) tool(b *block, width int) string {
	indent := "  "
	if b.nested {
		indent = "      "
	}
	var icon string
	switch b.state {
	case toolRunning:
		icon = sAccent.Render(r.spin)
	case toolDone:
		icon = sGreen.Render("●")
	case toolFailed:
		icon = sRed.Render("●")
	}

	head := icon + " " + sTool.Render(displayName(b.name))
	if s := r.summary(b.name, b.input); s != "" {
		head += " " + sDim.Render(truncate(s, width-len(indent)-len(b.name)-8))
	}
	if !b.finished.IsZero() {
		if d := b.finished.Sub(b.started); d > time.Second {
			head += sFaint.Render(fmt.Sprintf("  %.1fs", d.Seconds()))
		}
	}

	lines := []string{indent + head}
	body := r.toolBody(b, width-len(indent)-4)
	for i, l := range body {
		gutter := "   "
		if i == 0 {
			gutter = " ⎿ "
		}
		lines = append(lines, indent+sFaint.Render(gutter)+l)
	}
	return strings.Join(lines, "\n")
}

// toolBody renders the details under a tool call: diffs for edits, todo
// lists, and a preview of the output.
func (r *renderer) toolBody(b *block, width int) []string {
	expand := r.expand || b.open
	maxLines := 4
	if expand {
		maxLines = 60
	}
	if b.state == toolFailed {
		// The first line says what failed (e.g. "Exit code 2"); the rest is
		// ordinary output and stays readable in the normal colour.
		lines := wrapLines(b.result, width, sDim)
		if len(lines) > 0 {
			first, _, _ := strings.Cut(strings.TrimSpace(b.result), "\n")
			lines[0] = sRed.Render(truncate(first, width))
		}
		return clip(lines, maxLines*2)
	}
	switch b.name {
	case "Edit", "MultiEdit":
		return clip(r.editDiff(b.input, width), maxLines*4)
	case "Write":
		if b.state == toolRunning || expand {
			content, _ := b.input["content"].(string)
			return clip(diffLines("", content, width), maxLines*3)
		}
		content, _ := b.input["content"].(string)
		return []string{sDim.Render(plural(strings.Count(strings.TrimRight(content, "\n"), "\n")+1, "wrote %d line"))}
	case "TodoWrite":
		return todoLines(b.input)
	case "TaskCreate", "TaskUpdate", "ToolSearch":
		if b.state == toolDone && !expand {
			return nil // the header already says it all
		}
	case "Read":
		if b.state == toolDone && !expand {
			return []string{sDim.Render(plural(strings.Count(b.result, "\n")+1, "read %d line"))}
		}
	}
	if b.state == toolRunning || b.result == "" {
		return nil
	}
	return clip(wrapLines(b.result, width, sDim), maxLines)
}

// unifiedDiff colours a unified diff, dropping the file headers.
func unifiedDiff(d string, width int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "Index:"), strings.HasPrefix(l, "==="),
			strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "\\"):
		case strings.HasPrefix(l, "@@"):
			out = append(out, sFaint.Render(truncate(l, width)))
		case strings.HasPrefix(l, "+"):
			out = append(out, sDiffAdd.Width(width).Render("+ "+truncate(l[1:], width-2)))
		case strings.HasPrefix(l, "-"):
			out = append(out, sDiffDel.Width(width).Render("- "+truncate(l[1:], width-2)))
		default:
			out = append(out, sDim.Render("  "+truncate(strings.TrimPrefix(l, " "), width-2)))
		}
	}
	return out
}

func plural(n int, format string) string {
	s := fmt.Sprintf(format, n)
	if n != 1 {
		s += "s"
	}
	return s
}

func (r *renderer) editDiff(input map[string]any, width int) []string {
	if edits, ok := input["edits"].([]any); ok {
		var out []string
		for i, e := range edits {
			m, _ := e.(map[string]any)
			if i > 0 {
				out = append(out, sFaint.Render("⋯"))
			}
			old, _ := m["old_string"].(string)
			nw, _ := m["new_string"].(string)
			out = append(out, diffLines(old, nw, width)...)
		}
		return out
	}
	old, _ := input["old_string"].(string)
	nw, _ := input["new_string"].(string)
	return diffLines(old, nw, width)
}

// diffLines shows removed then added lines with a trimmed common prefix and
// suffix, which is how most edits read naturally.
func diffLines(old, nw string, width int) []string {
	a, b := splitLines(old), splitLines(nw)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []string
	if pre > 0 {
		out = append(out, sDim.Render("  "+truncate(a[pre-1], width-2)))
	}
	for _, l := range a[pre : len(a)-suf] {
		out = append(out, sDiffDel.Width(width).Render("- "+truncate(l, width-2)))
	}
	for _, l := range b[pre : len(b)-suf] {
		out = append(out, sDiffAdd.Width(width).Render("+ "+truncate(l, width-2)))
	}
	if suf > 0 {
		out = append(out, sDim.Render("  "+truncate(a[len(a)-suf], width-2)))
	}
	return out
}

type task struct{ id, subject, status string }

// todoLines renders the legacy TodoWrite input.
func todoLines(input map[string]any) []string { return taskLines(todoTasks(input)) }

func taskLines(tasks []task) []string {
	var out []string
	for _, t := range tasks {
		content := t.subject
		switch t.status {
		case "completed":
			out = append(out, sGreen.Render("☑ ")+sDim.Strikethrough(true).Render(content))
		case "in_progress":
			out = append(out, sAccent.Render("◐ ")+sBold.Render(content))
		default:
			out = append(out, sDim.Render("☐ "+content))
		}
	}
	return out
}

func (r *renderer) summary(name string, in map[string]any) string {
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch name {
	case "Bash":
		return firstLine(str("command"))
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		p := str("file_path")
		if p == "" {
			p = str("notebook_path")
		}
		return r.rel(p)
	case "Grep":
		s := str("pattern")
		if p := str("path"); p != "" {
			s += "  in " + r.rel(p)
		}
		return s
	case "Glob":
		return str("pattern")
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case "Task", "Agent":
		return str("description")
	case "Skill":
		return str("skill")
	case "ToolSearch":
		return str("query")
	case "TaskCreate":
		return str("subject")
	case "TaskUpdate":
		id, _ := in["taskId"].(string)
		if st := str("status"); st != "" {
			return "#" + id + " → " + strings.ReplaceAll(st, "_", " ")
		}
		return "#" + id
	case "TodoWrite":
		return ""
	}
	// Otherwise show the first short string argument, which is usually the
	// most meaningful one (a path, a query, a name).
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := in[k].(string); ok && len(s) < 120 {
			return s
		}
	}
	if len(in) == 0 {
		return ""
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func (r *renderer) rel(p string) string {
	if p == "" || r.cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(r.cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func displayName(name string) string {
	// mcp__server__tool → server · tool
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.SplitN(strings.TrimPrefix(name, "mcp__"), "__", 2)
		if len(parts) == 2 {
			return parts[0] + " · " + parts[1]
		}
	}
	return name
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func wrapLines(s string, width int, st lipgloss.Style) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\t", "    "), "\n ")
	if s == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, st.Render(truncate(l, width)))
	}
	return out
}

func clip(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	more := len(lines) - n
	return append(lines[:n:n], sFaint.Render(fmt.Sprintf("… +%d lines · click or ctrl+o to expand", more)))
}

func firstLine(s string) string {
	l, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if rest != "" {
		l += " …"
	}
	return l
}

// truncate shortens s to w cells with an ellipsis. It is ANSI-aware, so
// styled text (like sidebar rows) is cut between characters, never inside a
// colour escape sequence.
func truncate(s string, w int) string {
	if w < 4 {
		w = 4
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}
