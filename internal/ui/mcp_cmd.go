package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

type mcpApprovedMsg struct{ err error }

// showMCP lists the Direct API engine's MCP servers, and offers to start a
// project's servers when they're waiting for approval. The Claude Code
// engine manages its own servers, so /mcp goes to it.
func (m *Model) showMCP() tea.Cmd {
	eng, ok := m.client.(*agent.Engine)
	if !ok {
		return m.send("/mcp")
	}
	st := eng.MCPStatus()
	var sb strings.Builder
	sb.WriteString("**MCP servers**\n\n")
	if len(st) == 0 {
		sb.WriteString("None configured.\n")
	}
	pending := false
	for _, s := range st {
		mark := "✗"
		switch {
		case s.State == "connected":
			mark = "●"
		case s.State == "needs approval":
			mark, pending = "○", true
		case s.State == "starting":
			mark = "…"
		}
		line := fmt.Sprintf("- %s **%s**", mark, s.Name)
		if s.Scope != "" {
			line += " (" + s.Scope + ")"
		}
		line += " · " + s.State
		if len(s.Tools) > 0 {
			line += fmt.Sprintf(" · %d tools", len(s.Tools))
		}
		if s.Target != "" {
			line += " · `" + s.Target + "`"
		}
		sb.WriteString(line + "\n")
	}
	fmt.Fprintf(&sb, "\nAdd servers in `%s` (yours) or `.mcp.json` in the project, in the same format as Claude Code.",
		filepath.Join(configDir(), "mcp.json"))
	m.add(&block{kind: kindCmdOut, text: sb.String()})
	m.refresh()
	if pending {
		m.openPicker("Start this project's MCP servers?", []choice{
			{label: "Approve and start", desc: "they run programs on your computer: only approve a project you trust",
				run: func(m *Model) tea.Cmd {
					m.note("starting MCP servers…", true)
					return func() tea.Msg { return mcpApprovedMsg{eng.ApproveProjectMCP()} }
				}},
			{label: "Not now", desc: "ask again with /mcp", run: func(*Model) tea.Cmd { return nil }},
		}, 0)
	}
	return nil
}

func (m *Model) handleMCPApproved(msg mcpApprovedMsg) tea.Cmd {
	if msg.err != nil {
		m.add(&block{kind: kindError, text: "MCP: " + msg.err.Error()})
		m.refresh()
		return nil
	}
	return m.showMCP()
}

// noteProjectMCP says once when the project has MCP servers that are waiting
// for approval.
func (m *Model) noteProjectMCP() {
	if m.mcpNoted || m.engine != "api" {
		return
	}
	m.mcpNoted = true
	if names := agent.PendingProjectMCP(m.cwd, configDir()); len(names) > 0 {
		m.add(&block{kind: kindInfo, text: fmt.Sprintf(
			"This project's .mcp.json defines MCP servers (%s). They run programs on your computer, so they're off until you approve them: /mcp",
			strings.Join(names, ", "))})
	}
}
