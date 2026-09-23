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

	"github.com/stawan15/teveus/internal/agent"
)

// aliases map short names to commands, so "/q" suggests and runs /exit.
var aliases = map[string]string{
	"q": "exit", "quit": "exit",
	"new": "clear", "reset": "clear",
	"r": "resume", "continue": "resume", "sessions": "resume",
	"?": "help", "h": "help",
	"prefs": "settings", "config": "settings", "preferences": "settings",
	"m": "model",
	"u": "undo",
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
			fmt.Fprintf(&sb, "## You\n\n%s\n\n", b.text)
		case kindAssistant:
			fmt.Fprintf(&sb, "%s\n\n", b.text)
		case kindTool:
			fmt.Fprintf(&sb, "- **%s** %s\n", b.name, m.r.summary(b.name, b.input))
		case kindCmdOut:
			fmt.Fprintf(&sb, "%s\n\n", b.text)
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
