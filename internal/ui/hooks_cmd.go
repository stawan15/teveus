package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// showHooks lists the Direct API engine's hooks. Claude Code keeps its own,
// so the command goes to it on that engine.
func (m *Model) showHooks() tea.Cmd {
	eng, ok := m.client.(*agent.Engine)
	if !ok {
		return m.send("/hooks")
	}
	set, path := eng.Hooks()
	var events []string
	for ev := range set {
		events = append(events, ev)
	}
	sort.Strings(events)
	var sb strings.Builder
	sb.WriteString("**Hooks**\n\n")
	if len(events) == 0 {
		sb.WriteString("None yet.\n")
	}
	for _, ev := range events {
		for _, g := range set[ev] {
			match := g.Matcher
			if match == "" {
				match = "*"
			}
			for _, h := range g.Hooks {
				fmt.Fprintf(&sb, "- %s `%s`: `%s`\n", ev, match, h.Command)
			}
		}
	}
	fmt.Fprintf(&sb, "\nHooks run your own commands around tool calls: `PreToolUse` (exit code 2 blocks the call) and `PostToolUse` (a failing hook's output goes back to the model). "+
		"Add them to `%s`, in Claude Code's format, for example:\n\n```json\n{\"hooks\": {\"PostToolUse\": [{\"matcher\": \"Edit|Write\", \"hooks\": [{\"type\": \"command\", \"command\": \"gofmt -l .\"}]}]}}\n```\n"+
		"Hooks are kept there, never in your project, so a repository can't run commands by itself.", path)
	m.add(&block{kind: kindCmdOut, text: sb.String()})
	m.refresh()
	return nil
}
