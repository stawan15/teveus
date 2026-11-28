package ui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	glamansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
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

	// subagent telemetry (populated on Task / Agent parent blocks)
	subTools  int
	subTokIn  int
	subTokOut int

	cache  string
	cacheW int
	dirty  bool

	// A reply that is still arriving is laid out once up to its last newline;
	// wrappedLen says how much of text that covers. See streamingText.
	wrapped    string
	wrappedLen int
	wrappedW   int

	// phone is the line this block becomes on the phone page, kept until the
	// block changes the way the drawn text is. A snapshot runs on every frame
	// while a reply streams and has to redact the text to build one; the
	// pattern scan over a long conversation came to 52 ms a frame, which is
	// the whole budget and then some.
	phone    phoneLine
	phoneOK  bool
	phoneKey string
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
		st := markdownStyle()
		md, err := glamour.NewTermRenderer(
			glamour.WithStyles(st),
			glamour.WithWordWrap(width-2),
			glamour.WithEmoji(),
		)
		if err != nil {
			return s
		}
		r.md, r.mdW = md, width
	}
	clean, _ := stripMarkdownLinks(s)
	out, err := r.md.Render(clean)
	if err != nil {
		return s
	}
	return listGap(strings.Trim(out, "\n"))
}

// listGap puts a blank line between the items of a list whose items wrap. A
// bullet that runs onto a second line used to sit a single space from the next
// bullet, so six wrapped points read as one paragraph; the gap marks where each
// item ends. A list of single-line bullets needs none — it already reads as a
// list — so those are left dense.
func listGap(out string) string {
	if !strings.Contains(out, "\n") {
		return out
	}
	lines := strings.Split(out, "\n")
	gapped := make([]string, 0, len(lines))
	inList, itemWraps, inFence := false, false, false
	for _, l := range lines {
		plain := ansi.Strip(l)
		if strings.HasPrefix(strings.TrimSpace(plain), "```") {
			inFence = !inFence
		}
		switch {
		case inFence:
			// Code is shown as written, bullets inside it included.
			gapped = append(gapped, l)
		case strings.TrimSpace(plain) == "":
			// A blank line ends the list; glamour already put one here.
			inList, itemWraps = false, false
			gapped = append(gapped, l)
		case isBullet(plain):
			if inList && itemWraps {
				gapped = append(gapped, "")
			}
			inList, itemWraps = true, false
			gapped = append(gapped, l)
		case inList && wrapped(plain):
			// The tail of the item above.
			itemWraps = true
			gapped = append(gapped, l)
		default:
			inList, itemWraps = false, false
			gapped = append(gapped, l)
		}
	}
	return strings.Join(gapped, "\n")
}

func isBullet(s string) bool {
	t := strings.TrimLeft(s, " ")
	return strings.HasPrefix(t, "• ") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")
}

// wrapped reports whether l continues the list item above it rather than
// starting something of its own.
func wrapped(plain string) bool {
	for _, mark := range []string{"#", "|", "```", ">", "---"} {
		if strings.HasPrefix(strings.TrimLeft(plain, " "), mark) {
			return false
		}
	}
	return true
}

// markdownStyle keeps replies in the same palette as the rest of the UI.
func markdownStyle() glamansi.StyleConfig {
	st := styles.DarkStyleConfig
	if !theme.Dark {
		st = styles.LightStyleConfig
	}
	str := func(s string) *string { return &s }
	st.Document.Color = str(theme.Text)
	// One left edge for the whole reply. glamour indents each block by its own
	// margin on top of the document's, so a heading started at column 2, prose
	// at 2, a table at 3 and code at 4 — the reply looked torn away from the
	// edge, and a table pulled away from the prose above it. Zeroing them puts
	// every block on the same column; the transcript's own gutter supplies the
	// inset.
	zero := uint(0)
	st.Document.Margin = &zero
	st.Paragraph.Margin = &zero
	st.Heading.Margin = &zero
	st.Table.Indent, st.Table.Margin = &zero, &zero
	st.CodeBlock.Indent, st.CodeBlock.Margin = &zero, &zero
	// Nesting is the one indent worth keeping: a sub-point set flush with its
	// parent reads as another top-level point. glamour has no hanging indent
	// for a wrapped bullet, so the continuation line is marked instead, below.
	st.List.LevelIndent = 2
	st.Heading.Color = str(theme.Accent2)
	// The rank of a heading has to be visible without reading the text. H1 is
	// a filled band; H2 is the accent colour; H3 and below are dim, and the
	// "## " glamour prints in front of each is dropped because the weight and
	// colour already say it — the marks only added noise to every line.
	st.H1.Color = str(theme.Accent)
	st.H1.BackgroundColor = nil
	st.H1.Bold = boolPtr(true)
	st.H2.Color = str(theme.Accent2)
	st.H2.Bold = boolPtr(true)
	st.H2.Upper = boolPtr(false)
	st.H3.Color = str(theme.Text)
	st.H3.Bold = boolPtr(true)
	st.H4.Color = str(theme.Dim)
	st.H5.Color = str(theme.Dim)
	st.H6.Color = str(theme.Dim)
	st.H2.Prefix = ""
	st.H3.Prefix = ""
	st.H4.Prefix = ""
	st.H5.Prefix = ""
	st.H6.Prefix = ""
	st.BlockQuote.IndentToken = str("  ")
	st.BlockQuote.Color = str(theme.Dim)
	st.BlockQuote.Italic = boolPtr(true)
	st.Link.Color = str(theme.Accent2)
	st.LinkText.Color = str(theme.Accent)
	st.Code.Prefix = ""
	st.Code.Suffix = ""
	st.Code.Color = str(theme.Accent)
	st.Code.BackgroundColor = nil
	st.CodeBlock.Color = str(theme.Text)
	st.HorizontalRule.Color = str(theme.Border)
	st.CodeBlock.Chroma = &glamansi.Chroma{
		Text:                glamansi.StylePrimitive{Color: str(theme.Text)},
		Error:               glamansi.StylePrimitive{Color: str(theme.OnAccent), BackgroundColor: str(theme.Red)},
		Comment:             glamansi.StylePrimitive{Color: str(theme.Dim)},
		CommentPreproc:      glamansi.StylePrimitive{Color: str(theme.Yellow)},
		Keyword:             glamansi.StylePrimitive{Color: str(theme.Blue)},
		KeywordReserved:     glamansi.StylePrimitive{Color: str(theme.Accent2)},
		KeywordNamespace:    glamansi.StylePrimitive{Color: str(theme.Accent2)},
		KeywordType:         glamansi.StylePrimitive{Color: str(theme.Accent)},
		Operator:            glamansi.StylePrimitive{Color: str(theme.Dim)},
		Punctuation:         glamansi.StylePrimitive{Color: str(theme.Dim)},
		Name:                glamansi.StylePrimitive{Color: str(theme.Text)},
		NameBuiltin:         glamansi.StylePrimitive{Color: str(theme.Accent)},
		NameTag:             glamansi.StylePrimitive{Color: str(theme.Blue)},
		NameAttribute:       glamansi.StylePrimitive{Color: str(theme.Yellow)},
		NameClass:           glamansi.StylePrimitive{Color: str(theme.Text), Bold: boolPtr(true), Underline: boolPtr(true)},
		NameDecorator:       glamansi.StylePrimitive{Color: str(theme.Yellow)},
		NameFunction:        glamansi.StylePrimitive{Color: str(theme.Accent)},
		LiteralNumber:       glamansi.StylePrimitive{Color: str(theme.Yellow)},
		LiteralString:       glamansi.StylePrimitive{Color: str(theme.Green)},
		LiteralStringEscape: glamansi.StylePrimitive{Color: str(theme.Accent)},
		GenericDeleted:      glamansi.StylePrimitive{Color: str(theme.Red)},
		GenericEmph:         glamansi.StylePrimitive{Italic: boolPtr(true)},
		GenericInserted:     glamansi.StylePrimitive{Color: str(theme.Green)},
		GenericStrong:       glamansi.StylePrimitive{Bold: boolPtr(true)},
		GenericSubheading:   glamansi.StylePrimitive{Color: str(theme.Dim)},
		Background:          glamansi.StylePrimitive{BackgroundColor: str(theme.Surface)},
	}
	return st
}

func boolPtr(b bool) *bool { return &b }

func (r *renderer) render(b *block, width int) string {
	if !b.needsPaint(width) {
		return b.cache
	}
	var out string
	switch b.kind {
	case kindUser:
		out = sUser.Width(width).Render(termSafe(b.text))
	case kindAssistant:
		if b.streaming {
			// Raw text while streaming; markdown once the block is complete.
			out = r.streamingText(b, width) + sAccent.Render(" ▍")
		} else {
			out = r.markdown(termSafe(b.text), width)
		}
	case kindTool:
		out = r.tool(b, width)
	case kindInfo:
		out = sDim.Italic(true).Width(width).Render(termSafe(b.text))
	case kindError:
		out = sRed.Width(width).Render("✗ " + termSafe(b.text))
	case kindTurnEnd:
		out = renderTurnReceipt(termSafe(b.text), width)
	case kindCmdOut:
		// Command output is often Markdown (tables, lists): render it, set
		// off by a gutter so it reads as output rather than a reply. A line
		// markdown couldn't break — a long path or URL with no spaces — is
		// broken here, before the gutter goes on: left to the frame's final
		// fit, it would be split without its "│" and the output would look
		// like it had leaked out of the box.
		inner := max(width-2, 4)
		var lines []string
		for _, l := range strings.Split(r.markdown(strings.TrimSpace(termSafe(b.text)), width-2), "\n") {
			for _, piece := range strings.Split(ansi.Hardwrap(l, inner, false), "\n") {
				lines = append(lines, "  "+piece)
			}
		}
		out = strings.Join(lines, "\n")
	}
	if b.streaming {
		// streamingText already breaks every line to the column, and there are
		// no links or padding colours to strip: fitting it again would cut the
		// whole reply a second time on every chunk.
		b.cache, b.cacheW, b.dirty = out, width, false
		return b.cache
	}
	if !b.streaming {
		_, mdLinks := stripMarkdownLinks(b.text)
		out = linkifyLabels(out, mdLinks)
		out = linkify(out, b.text+"\n"+b.result)
	}
	// Last line of defence: nothing may draw past its column, or the sidebar
	// beside it is pushed sideways.
	b.cache, b.cacheW, b.dirty = fitWidth(stripPaddingColour(out), width), width, false
	return b.cache
}

// needsPaint reports whether render would do any work for this block.
func (b *block) needsPaint(width int) bool {
	return b.dirty || b.cacheW != width || b.cache == ""
}

// streamingText lays out a reply that is still arriving. Wrapping the whole
// text on every chunk was the slow part of a long answer: lipgloss re-wrapped
// 4,000 words each time a few more arrived, over a millisecond per chunk and
// growing with the answer.
//
// A newline restarts the wrap, so everything up to the last newline lays out
// the same no matter what follows: that part is wrapped once and kept, and
// only the line still being written is wrapped again. A chunk almost always
// falls inside a line, so the work per chunk is one line's worth instead of
// the whole reply.
func (r *renderer) streamingText(b *block, width int) string {
	const pad = 2
	body := max(width-pad, 1)
	// Fence markers are dropped while streaming: the raw text has no markdown,
	// so a fence would show as a line of backticks next to nothing.
	wrap := func(s string) string { return sText.Width(body).PaddingLeft(pad).Render(streamText(s)) }

	if b.wrappedW != width {
		b.wrapped, b.wrappedLen, b.wrappedW = "", 0, width
	}
	// Settle whole lines: a line that is still open can still gain words.
	if cut := strings.LastIndexByte(b.text, '\n') + 1; cut > b.wrappedLen && cut <= len(b.text) {
		if b.wrapped != "" {
			b.wrapped += "\n"
		}
		b.wrapped += wrap(b.text[b.wrappedLen:cut])
		b.wrappedLen = cut
	}
	if tail := b.text[min(b.wrappedLen, len(b.text)):]; tail != "" {
		if b.wrapped == "" {
			return wrap(tail)
		}
		return b.wrapped + "\n" + wrap(tail)
	}
	return b.wrapped
}

// stripPaddingColour removes the foreground-colour codes glamour wraps around
// the blanks that fill text out to the wrap width. It sets a colour, writes one
// space and resets, once per cell: a six-line reply came to 1,300 escape
// sequences and 15 KB, and most of every frame was those blanks. A space drawn
// in a text colour over the terminal's own background looks the same as a plain
// space, so dropping the codes changes nothing on screen.
//
// Only foreground codes are matched: a background or underline around the
// blanks is part of the drawing (Zebra-striped code blocks, selection marks)
// and stays.
func stripPaddingColour(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	// One pass per line, and only over lines that can match: this runs once per
	// block render, never per frame.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.Contains(l, "\x1b[0m") {
			lines[i] = paddingRuns.ReplaceAllString(l, "$1")
		}
	}
	return strings.Join(lines, "\n")
}

// paddingRuns matches a foreground-only colour set, a run of spaces, and the
// reset that closes it. Sequences that also set a background (e.g. ;48;2;…) are
// intentionally excluded so tinted bars and diff lines keep their fill.
var paddingRuns = regexp.MustCompile(`\x1b\[(?:38;(?:5;[0-9]+|2;[0-9]+;[0-9]+;[0-9]+)|3[0-9]|9[0-9])m( +)\x1b\[0m`)

func (r *renderer) tool(b *block, width int) string {
	// No indent of its own: the transcript's one-cell gutter already sets the
	// left edge, so a tool call starts in the same column as prose and code.
	// Only a nested subagent call steps in, under the Task call that spawned it.
	indent := ""
	if b.nested {
		indent = "      "
	}
	var icon string
	isSub := !b.nested && (b.name == "Task" || b.name == "Agent")
	switch b.state {
	case toolRunning:
		if isSub {
			icon = sAccent2.Render(r.spin)
		} else {
			icon = sAccent.Render(r.spin)
		}
	case toolDone:
		if isSub {
			icon = sAccent2.Render("◇")
		} else {
			icon = sGreen.Render("●")
		}
	case toolFailed:
		icon = sRed.Render("✗")
	}

	var tail string
	if isSub && (b.subTools > 0 || b.subTokIn+b.subTokOut > 0) {
		var meta []string
		if b.subTools > 0 {
			meta = append(meta, plural(b.subTools, "%d tool"))
		}
		if tot := b.subTokIn + b.subTokOut; tot > 0 {
			meta = append(meta, fmtTokens(tot)+" tok")
		}
		tail += sFaint.Render("  · " + strings.Join(meta, " · "))
	}
	if b.state == toolFailed {
		tail += "  " + softPill("failed", cDiffDel, cRed)
	}
	if !b.finished.IsZero() {
		if d := b.finished.Sub(b.started); d > time.Second {
			tail += sFaint.Render(fmt.Sprintf("  %.1fs", d.Seconds()))
		}
	} else if b.state == toolRunning && !b.started.IsZero() {
		if d := time.Since(b.started); d >= time.Second {
			tail += sFaint.Render(fmt.Sprintf("  %.1fs", d.Seconds()))
		}
	}
	toolStyle := sTool
	if isSub {
		toolStyle = sAccent2.Bold(true)
	}
	headPrefix := icon + " " + toolStyle.Render(displayName(b.name))
	head := headPrefix
	if s := r.summary(b.name, b.input); s != "" {
		// Budget summary width against the full prefix AND trailing badges/duration
		// so long commands with a "failed" pill never overflow or smear colour.
		avail := max(width-len(indent)-ansi.StringWidth(headPrefix)-ansi.StringWidth(tail)-2, 6)
		head += " " + r.styleSummary(b.name, truncate(s, avail))
	}
	head += tail

	lines := []string{indent + head}
	body := r.toolBody(b, width-len(indent)-4)
	branch := sFaint.Render("  ⎿ ")
	if b.state == toolRunning && isSub {
		branch = sAccent2.Render("  ⎿ ")
	} else if b.state == toolRunning && (b.name == "Edit" || b.name == "MultiEdit" || b.name == "Write") {
		branch = sYellow.Render("  ⎿ ")
	} else if b.state == toolFailed {
		branch = sRed.Render("  ⎿ ")
	}
	for i, l := range body {
		prefix := "    "
		if i == 0 {
			prefix = branch
		}
		lines = append(lines, indent+prefix+l)
	}
	return strings.Join(lines, "\n")
}

// renderTurnReceipt draws a whisper-quiet hairline receipt at the end of a turn
// (Peak-End Rule): duration, tokens used, cache efficiency, cost and tools.
func renderTurnReceipt(text string, width int) string {
	parts := strings.Split(text, " · ")
	styled := make([]string, 0, len(parts))
	for _, p := range parts {
		switch {
		case strings.Contains(p, "% cached"):
			styled = append(styled, sAccent.Render(p))
		case strings.HasPrefix(p, "$"):
			styled = append(styled, sText.Render(p))
		default:
			styled = append(styled, sDim.Render(p))
		}
	}
	core := sBorder.Render("── ") + sGreen.Render("✓ ") + strings.Join(styled, sFaint.Render(" · "))
	target := min(width, 80)
	if pad := target - ansi.StringWidth(core) - 1; pad >= 4 {
		core += " " + sBorder.Render(strings.Repeat("─", pad))
	}
	return core
}

// styleSummary highlights the filename while dimming the parent directory path
// so file-oriented tool calls scan faster (Law of Figure-Ground).
func (r *renderer) styleSummary(name, s string) string {
	switch name {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		if i := strings.LastIndexAny(s, "/\\"); i >= 0 && i+1 < len(s) {
			return sFaint.Render(s[:i+1]) + sText.Render(s[i+1:])
		}
		return sText.Render(s)
	}
	return sDim.Render(s)
}

// isReadTool reports whether b is a completed, collapsed read-only inspection
// call that can fold into a single scannable chain with its neighbours.
func isReadTool(b *block) bool {
	if b.kind != kindTool || b.nested || b.state != toolDone || b.open {
		return false
	}
	switch b.name {
	case "Read", "Grep", "Glob":
		return true
	}
	return false
}

// renderReadChain folds consecutive read-only calls into one compact row
// (client-side only; agent history and prompt caches are untouched).
func (r *renderer) renderReadChain(chain []*block, width int) string {
	label := chain[0].name
	for _, b := range chain[1:] {
		if b.name != label {
			label = "Inspected"
			break
		}
	}
	head := sGreen.Render("●") + " " + sTool.Render(fmt.Sprintf("%s ×%d", label, len(chain)))
	var items []string
	var totalDur time.Duration
	for _, b := range chain {
		if !b.finished.IsZero() && !b.started.IsZero() && b.finished.After(b.started) {
			totalDur += b.finished.Sub(b.started)
		}
		s := r.summary(b.name, b.input)
		if b.name == "Read" {
			base := filepath.Base(s)
			n := strings.Count(b.result, "\n") + 1
			items = append(items, sText.Render(base)+sFaint.Render(fmt.Sprintf(" (%dL)", n)))
		} else if s != "" {
			items = append(items, sDim.Render(truncate(s, 22)))
		}
	}
	suffix := sFaint.Render("  [^O]")
	if totalDur >= 200*time.Millisecond {
		suffix = sFaint.Render(fmt.Sprintf("  %.1fs  [^O]", totalDur.Seconds()))
	}
	avail := width - ansi.StringWidth(head) - ansi.StringWidth(suffix) - 2
	var shown []string
	used := 0
	for i, it := range items {
		w := ansi.StringWidth(it)
		sepW := 0
		if i > 0 {
			sepW = 3
		}
		if used+sepW+w > avail {
			rem := len(items) - i
			if rem > 0 && avail-used >= 8 {
				shown = append(shown, sFaint.Render(fmt.Sprintf("+%d more", rem)))
			}
			break
		}
		shown = append(shown, it)
		used += sepW + w
	}
	mid := ""
	if len(shown) > 0 {
		mid = "  " + strings.Join(shown, sFaint.Render(" · "))
	}
	return fitWidth(head+mid+suffix, width)
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
	if b.result == "" {
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
// suffix, highlighting intra-line changed spans on 1-to-1 replacements so the
// eye lands directly on what changed (Von Restorff Effect).
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
	delRows := a[pre : len(a)-suf]
	addRows := b[pre : len(b)-suf]
	if len(delRows) == len(addRows) && len(delRows) > 0 && len(delRows) <= 8 {
		dels := make([]string, len(delRows))
		adds := make([]string, len(addRows))
		for i := range delRows {
			dels[i], adds[i] = renderInlineDiff(delRows[i], addRows[i], width)
		}
		out = append(out, dels...)
		out = append(out, adds...)
	} else {
		for _, l := range delRows {
			out = append(out, sDiffDel.Width(width).Render("- "+truncate(l, width-2)))
		}
		for _, l := range addRows {
			out = append(out, sDiffAdd.Width(width).Render("+ "+truncate(l, width-2)))
		}
	}
	if suf > 0 {
		out = append(out, sDim.Render("  "+truncate(a[len(a)-suf], width-2)))
	}
	return out
}

// renderInlineDiff paints a paired removed/added line with brighter emphasis
// on the exact differing span. Runs only during cached block rendering.
func renderInlineDiff(oldLine, newLine string, width int) (string, string) {
	w := max(width-2, 4)
	clean := func(s string) string { return strings.ReplaceAll(termSafe(s), "\t", "    ") }
	oa, ob := []rune(truncate(clean(oldLine), w)), []rune(truncate(clean(newLine), w))
	cp := 0
	for cp < len(oa) && cp < len(ob) && oa[cp] == ob[cp] {
		cp++
	}
	cs := 0
	for cs < len(oa)-cp && cs < len(ob)-cp && oa[len(oa)-1-cs] == ob[len(ob)-1-cs] {
		cs++
	}
	if cp == 0 && cs == 0 {
		return sDiffDel.Width(width).Render("- " + string(oa)),
			sDiffAdd.Width(width).Render("+ " + string(ob))
	}
	paint := func(mark string, rs []rune, base, hi lipgloss.Style) string {
		pre := string(rs[:cp])
		mid := string(rs[cp : len(rs)-cs])
		suf := string(rs[len(rs)-cs:])
		var sb strings.Builder
		sb.WriteString(base.Render(mark + pre))
		if mid != "" {
			sb.WriteString(hi.Render(mid))
		}
		if suf != "" {
			sb.WriteString(base.Render(suf))
		}
		row := sb.String()
		if pad := width - ansi.StringWidth(row); pad > 0 {
			row += base.Render(strings.Repeat(" ", pad))
		}
		return row
	}
	return paint("- ", oa, sDiffDel, sDiffDelHi), paint("+ ", ob, sDiffAdd, sDiffAddHi)
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

// streamText drops code-fence lines from a reply that is still arriving. The
// raw text has no markdown, so a fence would show as a line of backticks next
// to nothing; the finished block gets them back from its unmodified text.
func streamText(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// wrapLines wraps each line of s to width cells. Long words (URLs, paths,
// minified output) break rather than get cut, so a wide tool result keeps its
// text instead of turning into an ellipsis.
func wrapLines(s string, width int, st lipgloss.Style) []string {
	s = strings.TrimRight(strings.ReplaceAll(termSafe(s), "\t", "    "), "\n ")
	if s == "" {
		return nil
	}
	if width < 4 {
		width = 4
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for _, w := range strings.Split(ansi.Wrap(l, width, ""), "\n") {
			out = append(out, st.Render(w))
		}
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
	s = termSafe(s)
	if w < 4 {
		w = 4
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// lineWidth is ansi.StringWidth for the rows a frame is actually made of,
// without the grapheme walk that costs more than drawing the row. Plain ASCII
// is one cell a byte, and so is ASCII with colour codes around it; anything
// else (wide characters, combining marks, Thai) goes to the real measurement.
func lineWidth(s string) int {
	w := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			i = skipEscape(s, i)
		case c >= 0x20 && c <= 0x7e:
			w++
		default:
			return ansi.StringWidth(s)
		}
	}
	return w
}

// skipEscape returns the index of the last byte of the escape sequence that
// starts at s[i], which is always 0x1b.
func skipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return i
	}
	switch s[i+1] {
	case '[': // CSI: parameter bytes, then one final byte in @ to ~
		for j := i + 2; j < len(s); j++ {
			if c := s[j]; c >= 0x40 && c <= 0x7e {
				return j
			}
		}
		return len(s) - 1
	case ']': // OSC: runs to BEL or ST
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 1
			}
		}
		return len(s) - 1
	}
	return i + 1
}

// fitWidth hard-wraps any line of s wider than w, so text with no spaces to
// break on (Thai, Chinese, URLs) cannot draw past its column. lipgloss only
// wraps at word boundaries, and its Width is a minimum, not a clamp.
//
// Text that never passed through termSafe is decomposed here first: the width
// libraries count Thai and Lao SARA AM as one cell while terminals draw two, so
// a line they call narrow can still wrap on screen and shift everything below
// it. Every rendered line ends up going through this function, which makes it
// the one place that has to remember.
func fitWidth(s string, w int) string {
	if w < 1 {
		return s
	}
	lines := strings.Split(s, "\n")
	// Not lines[:0]: wrapping one line appends more entries than it consumes,
	// and reusing the array would overwrite lines that haven't been read yet,
	// dropping them from the result.
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		// Measured before decomposition, which is how the caller measured the
		// row it fitted to. Decomposition can turn a row that fitted into one a
		// cell too wide ("ทำ" is measured as one cell, drawn as two), and
		// wrapping it here would add a line and push the whole frame down by
		// one — that is what made the sidebar's border jump row by row. Cut the
		// last cell instead: the row count is what the layout depends on.
		rawW := ansi.StringWidth(l)
		safe := termSafe(l)
		switch safeW := ansi.StringWidth(safe); {
		case rawW <= w && safeW > w:
			out = append(out, ansi.Truncate(safe, w, ""))
		case safeW <= w:
			out = append(out, safe)
		default:
			out = append(out, strings.Split(ansi.Hardwrap(safe, w, false), "\n")...)
		}
	}
	return strings.Join(out, "\n")
}

// amSplitter rewrites Thai and Lao SARA AM as its compatibility decomposition
// (NIKHAHIT + SARA AA). The width libraries fold SARA AM into the previous
// grapheme and count "ทำ" as one cell, but terminals draw it as two, so lines
// holding it overflow, wrap, and push the whole screen out of place. The
// decomposed form looks the same and every library measures it correctly.
var amSplitter = strings.NewReplacer("\u0e33", "\u0e4d\u0e32", "\u0eb3", "\u0ecd\u0eb2")

// termSafe makes s measure the same in lipgloss as on screen.
func termSafe(s string) string {
	// U+0E33 and U+0EB3 are the only runes this looks for, so a byte scan for
	// their UTF-8 (e0 b8 b3 / e0 ba b3) answers faster than ContainsAny, which
	// decodes the whole string. Every frame row goes through here.
	for i := 0; i+2 < len(s); i++ {
		if s[i] == 0xe0 && s[i+2] == 0xb3 && (s[i+1] == 0xb8 || s[i+1] == 0xba) {
			return amSplitter.Replace(s)
		}
	}
	return s
}
