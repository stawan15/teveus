package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stawan15/teveus/internal/agent"
)

// aliases map short names to commands, so "/q" suggests and runs /exit.
var aliases = map[string]string{
	"q": "exit", "quit": "exit",
	"new": "clear", "reset": "clear",
	"r": "resume", "continue": "resume", "sessions": "resume",
	"?": "help", "h": "help",
	"prefs": "settings", "config": "settings", "preferences": "settings",
	"m":  "model",
	"u":  "undo",
	"rw": "rewind",
}

func aliasesOf(name string) []string {
	var out []string
	for a, n := range aliases {
		if n == name {
			out = append(out, a)
		}
	}
	return out
}

type shellResultMsg struct {
	cmd, out string
	err      error
	dur      time.Duration
}

// runShell runs "!command" in the project directory. On the API engine the
// output also goes into the conversation so the model can use it.
func (m *Model) runShell(command string) tea.Cmd {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	m.note("running "+command+"…", true)
	cwd := m.cwd
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		sh, flag := "bash", "-c"
		if runtime.GOOS == "windows" {
			sh, flag = "cmd", "/C"
		} else if _, err := exec.LookPath("bash"); err != nil {
			sh = "sh"
		}
		c := exec.CommandContext(ctx, sh, flag, command)
		c.Dir = cwd
		var buf bytes.Buffer
		c.Stdout, c.Stderr = &buf, &buf
		start := time.Now()
		err := c.Run()
		return shellResultMsg{cmd: command, out: buf.String(), err: err, dur: time.Since(start)}
	}
}

func (m *Model) handleShell(r shellResultMsg) {
	out := strings.TrimRight(r.out, "\n")
	if len(out) > 20000 {
		out = out[:10000] + "\n… (truncated) …\n" + out[len(out)-10000:]
	}
	status := "✓"
	if r.err != nil {
		status = "✗ " + r.err.Error()
	}
	text := fmt.Sprintf("`$ %s` %s · %s\n\n```\n%s\n```", r.cmd, status, fmtDur(r.dur), out)
	m.add(&block{kind: kindCmdOut, text: text})
	if eng, ok := m.client.(*agent.Engine); ok {
		eng.AddContext(fmt.Sprintf("I ran `%s` in my terminal (%s):\n```\n%s\n```", r.cmd, status, out))
		m.note("output added to the conversation", true)
	}
	m.refresh()
}

// undo reverts the last turn's file edits on the API engine.
func (m *Model) undo() tea.Cmd {
	eng, ok := m.client.(*agent.Engine)
	if !ok {
		m.note("/undo works on the Direct API engine; with Claude Code use git to revert", false)
		return nil
	}
	restored, err := eng.Undo()
	if err != nil && len(restored) == 0 {
		m.note(err.Error(), false)
		return nil
	}
	// Drop the undone exchange from the transcript.
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == kindUser {
			m.input.SetValue(m.blocks[i].text) // put the prompt back to edit and retry
			m.blocks = m.blocks[:i]
			break
		}
	}
	var names []string
	for _, p := range restored {
		if rel, err := filepath.Rel(m.cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
		names = append(names, p)
	}
	msg := "undid the last turn"
	if len(names) > 0 {
		msg += " · restored " + strings.Join(names, ", ")
	}
	if err != nil {
		msg += " · " + err.Error()
	}
	m.add(&block{kind: kindInfo, text: msg + " (shell commands aren't undone)"})
	m.afterInput()
	m.refresh()
	return nil
}

// rewind restores the conversation and files to before an earlier turn.
func (m *Model) rewind() tea.Cmd {
	eng, ok := m.client.(*agent.Engine)
	if !ok {
		m.note("/rewind works on the Direct API engine; with Claude Code use git to revert", false)
		return nil
	}
	points := eng.RewindPoints()
	if len(points) == 0 {
		m.note("nothing to rewind", false)
		return nil
	}
	m.openRewindPicker(eng, points, 0)
	return nil
}

func (m *Model) openRewindPicker(eng *agent.Engine, points []agent.RewindPoint, sel int) {
	cs := make([]choice, 0, len(points))
	for i := len(points) - 1; i >= 0; i-- {
		p := points[i]
		back := len(points) - i
		desc := plural(back, "%d turn") + " back"
		if p.Files > 0 {
			desc += " · " + plural(p.Files, "%d file")
		}
		label := strings.Join(strings.Fields(p.Prompt), " ")
		if label == "" {
			label = "(no prompt)"
		}
		label = truncate(label, 64)
		cs = append(cs, choice{label: label, desc: desc,
			run: func(m *Model) tea.Cmd { return m.reviewRewind(eng, p, points) }})
	}
	m.openPicker("Rewind to before…", cs, sel)
	if m.h >= 20 {
		m.pop.hint = truncate("Pick a turn to review; nothing changes yet", max(m.w-6, 20))
		m.layout()
	}
}

// reviewRewind shows what a turn changed and waits for confirmation. Nothing
// is touched until the user presses enter.
func (m *Model) reviewRewind(eng *agent.Engine, p agent.RewindPoint, points []agent.RewindPoint) tea.Cmd {
	prev, err := eng.PreviewRewind(p.Index)
	if err != nil {
		m.note(err.Error(), false)
		return nil
	}
	inner := max(m.w-6, 20)
	maxDiffLines := max(3, min(10, m.h-16))
	cs := make([]choice, 0, len(prev.Files)+1)
	byPath := map[string]agent.RewindFile{}
	for _, f := range prev.Files {
		byPath[f.Path] = f
		cs = append(cs, choice{
			label: m.rewindPath(f.Path),
			desc:  rewindFileDesc(f),
			color: rewindFileColor(f),
			value: f.Path,
			run:   func(m *Model) tea.Cmd { return m.rewindTo(eng, p.Index) },
		})
	}
	if len(cs) == 0 {
		cs = append(cs, choice{
			label: "No file changes",
			desc:  "only the conversation and prompt are rewound",
			color: cDim,
			run:   func(m *Model) tea.Cmd { return m.rewindTo(eng, p.Index) },
		})
	}

	hint := "Nothing changes until you press enter"
	if prompt := strings.Join(strings.Fields(p.Prompt), " "); prompt != "" {
		hint = truncate("“"+prompt+"”", inner)
	}
	back := 0
	for i, q := range points {
		if q.Index == p.Index {
			back = len(points) - 1 - i
			break
		}
	}

	m.pop = popup{mode: popReview, title: "Review rewind", hint: hint, all: cs, flat: true}
	m.pop.filter()
	m.pop.preview = func(m *Model, c choice) {
		if f, ok := byPath[c.value]; ok {
			body := []string{sAccent.Bold(true).Render(truncate(c.label, inner))}
			body = append(body, m.rewindDiff(f, inner, maxDiffLines)...)
			m.pop.body = append(body, sFaint.Render("Shell commands aren't undone."))
		} else {
			m.pop.body = []string{sDim.Render("No files will change; only the conversation and prompt are rewound."),
				sFaint.Render("Shell commands aren't undone.")}
		}
	}
	m.pop.back = func(m *Model) tea.Cmd {
		m.openRewindPicker(eng, points, back)
		return nil
	}
	m.previewSel()
	m.layout()
	return nil
}

func (m *Model) rewindPath(path string) string {
	if rel, err := filepath.Rel(m.cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func rewindFileColor(f agent.RewindFile) lipgloss.Color {
	switch {
	case f.Err != "":
		return cRed
	case f.Status == agent.RewindDeleted:
		return cRed
	case f.Status == agent.RewindRestored:
		return cGreen
	default:
		return cYellow
	}
}

func rewindFileDesc(f agent.RewindFile) string {
	desc := string(f.Status)
	switch {
	case f.Err != "":
		return desc + " · can't read it now"
	case f.Binary:
		return desc + " · binary file"
	case f.TooLarge:
		return desc + " · large file"
	}
	if f.Added > 0 {
		desc += fmt.Sprintf(" · +%d", f.Added)
	}
	if f.Removed > 0 {
		desc += fmt.Sprintf(" · −%d", f.Removed)
	}
	return desc
}

func (m *Model) rewindDiff(f agent.RewindFile, width, maxLines int) []string {
	switch {
	case f.Err != "":
		return []string{sDim.Render(truncate("No preview: "+f.Err+". The rewind will still try.", width))}
	case f.Binary:
		return []string{sDim.Render("Binary file · no text preview. The rewind will still apply.")}
	case f.TooLarge:
		return []string{sDim.Render("Large file · too large for a preview. The rewind will still apply.")}
	}
	lines := diffLines(string(f.Before), string(f.After), width)
	if len(lines) == 0 {
		return []string{sDim.Render("No visible text changes.")}
	}
	if len(lines) > maxLines {
		more := len(lines) - maxLines
		lines = append(lines[:maxLines:maxLines], sFaint.Render(fmt.Sprintf("… +%d lines", more)))
	}
	return lines
}

func (m *Model) rewindTo(eng *agent.Engine, index int) tea.Cmd {
	res, err := eng.RewindTo(index)
	if err != nil && len(res.History) == 0 && len(res.Restored) == 0 {
		m.note(err.Error(), false)
		return nil
	}
	m.blocks, m.tools, m.stream = nil, map[string]*block{}, nil
	m.todos, m.tasks = nil, nil
	m.replayAPI(res.History)
	m.input.SetValue(res.Prompt)
	var names []string
	for _, p := range res.Restored {
		names = append(names, m.rewindPath(p))
	}
	msg := "rewound to before that turn"
	if len(names) > 0 {
		msg += " · restored " + strings.Join(names, ", ")
	}
	if err != nil {
		msg += " · " + err.Error()
	}
	m.add(&block{kind: kindInfo, text: msg + " (shell commands aren't undone)"})
	m.afterInput()
	m.refresh()
	m.vp.GotoBottom()
	return nil
}

// gitDiff shows what changed in the working tree.
func (m *Model) gitDiff() tea.Cmd {
	cwd := m.cwd
	return func() tea.Msg {
		run := func(args ...string) string {
			c := exec.Command("git", args...)
			c.Dir = cwd
			out, err := c.CombinedOutput()
			if err != nil {
				return ""
			}
			return strings.TrimRight(string(out), "\n")
		}
		status := run("status", "--short")
		if status == "" {
			if run("rev-parse", "--is-inside-work-tree") == "" {
				return shellResultMsg{cmd: "git status", out: "not a git repository", err: fmt.Errorf("no git")}
			}
			return diffMsg("Working tree clean: no changes.")
		}
		stat := run("diff", "--stat", "HEAD")
		return diffMsg(fmt.Sprintf("**Changes**\n\n```\n%s\n```\n\n```\n%s\n```", status, stat))
	}
}

type diffMsg string

func (m *Model) exportTranscript() tea.Cmd {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# teveus conversation\n\n%s · %s · %s\n\n", time.Now().Format("2006-01-02 15:04"), shortModel(m.model), m.cwd)
	for _, b := range m.blocks {
		switch b.kind {
		case kindUser:
			fmt.Fprintf(&sb, "## You\n\n%s\n\n", agent.Redact(b.text))
		case kindAssistant:
			fmt.Fprintf(&sb, "%s\n\n", agent.Redact(b.text))
		case kindTool:
			fmt.Fprintf(&sb, "- **%s** %s\n", b.name, agent.Redact(m.r.summary(b.name, b.input)))
		case kindCmdOut:
			fmt.Fprintf(&sb, "%s\n\n", agent.Redact(b.text))
		}
	}
	name := "teveus-" + time.Now().Format("20060102-150405") + ".md"
	path := filepath.Join(m.cwd, name)
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		m.note("export failed: "+err.Error(), false)
		return nil
	}
	m.note("exported to "+name, true)
	return nil
}

const initPrompt = `Analyze this repository and create (or improve, if it exists) an AGENTS.md at the project root for future coding agents. Include, only when you can verify them from the repo:
- what the project is, in 2-3 sentences
- exact commands to build, run, test (including a single test) and lint
- the code layout: key directories and what lives where
- conventions you observe (style, naming, error handling, testing patterns)
- gotchas a newcomer would trip on
Keep it concise (under ~150 lines), skip generic advice, and don't invent commands.`

func (m *Model) initProject() tea.Cmd {
	if m.engine == "claude" {
		return m.send("/init") // Claude Code's own /init
	}
	m.add(&block{kind: kindInfo, text: "/init · analysing the project and writing AGENTS.md"})
	return m.send(initPrompt)
}
