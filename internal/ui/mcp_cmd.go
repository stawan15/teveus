package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

type mcpApprovedMsg struct{ err error }

// mcpDoneMsg carries the result of adding or retrying a server.
type mcpDoneMsg struct {
	server string
	err    error
}

// showMCP lists the Direct API engine's MCP servers, and offers what each one
// needs: approval for a project's file, a token for a server that answered 401,
// a retry for one that failed to start, and adding one by hand. The Claude Code
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
	for _, s := range st {
		sb.WriteString(mcpLine(s) + "\n")
	}
	fmt.Fprintf(&sb, "\n`%s` holds yours; `.mcp.json` in the project is shared with the repo.\n",
		filepath.Join(configDir(), "mcp.json"))
	m.add(&block{kind: kindCmdOut, text: sb.String()})
	m.refresh()

	m.openPicker("MCP servers", m.mcpChoices(eng, st), 0)
	return nil
}

// mcpLine is one server as a list row: its state, what it runs, and its tools.
func mcpLine(s agent.MCPServerStatus) string {
	mark := "✗"
	switch {
	case s.State == "connected":
		mark = "●"
	case s.State == "needs approval":
		mark = "○"
	case s.State == "starting":
		mark = "…"
	}
	line := fmt.Sprintf("- %s **%s**", mark, s.Name)
	if s.Scope != "" {
		line += " (" + s.Scope + ")"
	}
	line += " · " + shortMCPState(s.State)
	if len(s.Tools) > 0 {
		line += fmt.Sprintf(" · %d tools: %s", len(s.Tools), strings.Join(s.Tools, ", "))
	}
	if s.Target != "" {
		line += "\n  `" + s.Target + "`"
	}
	return line
}

// shortMCPState keeps a failing server's message to one readable line: they
// can be a whole HTTP error with a stack of headers in them.
func shortMCPState(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// mcpChoices builds the per-server actions.
func (m *Model) mcpChoices(eng *agent.Engine, st []agent.MCPServerStatus) []choice {
	var cs []choice
	for _, s := range st {
		s := s
		switch {
		case s.State == "needs approval":
			cs = append(cs, choice{
				label: "Approve " + s.Name,
				desc:  "runs a program on this machine: only approve a project you trust",
				run: func(m *Model) tea.Cmd {
					m.note("starting MCP servers…", true)
					return func() tea.Msg { return mcpApprovedMsg{eng.ApproveProjectMCP()} }
				},
			})
		case needsAuth(s.State):
			cs = append(cs, choice{
				label: "Add a token for " + s.Name,
				desc:  "the server answered " + firstWord(s.State) + "; the token goes in its \"headers\"",
				run: func(m *Model) tea.Cmd {
					m.askMCPToken(eng, s)
					return nil
				},
			})
			cs = append(cs, choice{
				label: "Retry " + s.Name,
				desc:  "after its config or token changed on disk",
				run:   func(m *Model) tea.Cmd { return m.retryMCP(eng, s.Name) },
			})
		case s.State == "connected":
			cs = append(cs, choice{
				label: "Reconnect " + s.Name,
				desc:  fmt.Sprintf("connected, %d tools", len(s.Tools)),
				run:   func(m *Model) tea.Cmd { return m.retryMCP(eng, s.Name) },
			})
		case s.Scope == "project":
			cs = append(cs, choice{
				label: "Retry " + s.Name,
				desc:  "project server: " + shortMCPState(s.State),
				run:   func(m *Model) tea.Cmd { return m.retryMCP(eng, s.Name) },
			})
		}
	}
	cs = append(cs,
		choice{label: "Add a server", desc: "a stdio command or an http URL, saved to your config",
			run: func(m *Model) tea.Cmd {
				m.askMCPAdd(eng, "")
				return nil
			}},
		choice{label: "Show the config path", desc: filepath.Join(configDir(), "mcp.json"),
			run: func(m *Model) tea.Cmd {
				p := filepath.Join(configDir(), "mcp.json")
				m.add(&block{kind: kindInfo, text: "Your MCP config: " + p})
				m.refresh()
				copyToClipboard(p)
				m.note("copied path: "+p, true)
				return nil
			}},
	)
	return cs
}

// needsAuth reports a server that refused the connection for lack of a token.
func needsAuth(state string) bool {
	s := strings.ToLower(state)
	return strings.Contains(s, "401") || strings.Contains(s, "403") ||
		strings.Contains(s, "needs authorization") || strings.Contains(s, "unauthorized")
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return "401"
}

// askMCPAdd walks through adding a server to the user's own config: a name,
// then either a command or a URL.
func (m *Model) askMCPAdd(eng *agent.Engine, prefill string) {
	m.openInput("MCP server: name", "the name its tools are grouped under, e.g. github", false,
		func(m *Model, name string) tea.Cmd {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil
			}
			if _, err := agent.UserMCPConfig(configDir()); err != nil {
				m.note("mcp.json: "+err.Error(), false)
				return nil
			}
			m.openPicker(name+": how does it connect?", []choice{
				{label: "Run a command (stdio)", desc: "the usual form: npx, uvx or a binary",
					run: func(m *Model) tea.Cmd { m.askMCPCommand(eng, name); return nil }},
				{label: "Connect to a URL (http)", desc: "a Streamable HTTP endpoint",
					run: func(m *Model) tea.Cmd { m.askMCPURL(eng, name); return nil }},
			}, 0)
			return nil
		})
}

func (m *Model) askMCPCommand(eng *agent.Engine, name string) {
	m.openInput(name+": command", "e.g. npx -y @modelcontextprotocol/server-filesystem ~/notes", false,
		func(m *Model, line string) tea.Cmd {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) == 0 {
				return nil
			}
			return m.saveMCPServer(eng, name, agent.MCPConfig{Command: fields[0], Args: fields[1:]})
		})
}

func (m *Model) askMCPURL(eng *agent.Engine, name string) {
	m.openInput(name+": URL", "e.g. https://mcp.example.com/mcp", false,
		func(m *Model, url string) tea.Cmd {
			url = strings.TrimSpace(url)
			if url == "" {
				return nil
			}
			return m.saveMCPServer(eng, name, agent.MCPConfig{Type: "http", URL: url})
		})
}

// saveMCPServer writes the server into the user's config and connects it.
func (m *Model) saveMCPServer(eng *agent.Engine, name string, cfg agent.MCPConfig) tea.Cmd {
	cfgs, err := agent.UserMCPConfig(configDir())
	if err != nil {
		cfgs = map[string]agent.MCPConfig{}
	}
	cfgs[name] = cfg
	if err := agent.SaveUserMCPConfig(configDir(), cfgs); err != nil {
		m.add(&block{kind: kindError, text: "MCP: " + err.Error()})
		m.refresh()
		return nil
	}
	m.note("starting "+name+"…", true)
	return m.retryMCP(eng, name)
}

// askMCPToken sets a bearer token on a server that answered 401. It is masked,
// written to the user's own mcp.json (0600) and never echoed back.
func (m *Model) askMCPToken(eng *agent.Engine, s agent.MCPServerStatus) {
	m.openInput("Token for "+s.Name, "stored in your mcp.json as an Authorization header", true,
		func(m *Model, tok string) tea.Cmd {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				return nil
			}
			cfgs, err := agent.UserMCPConfig(configDir())
			if err != nil {
				m.add(&block{kind: kindError, text: "MCP: " + err.Error()})
				m.refresh()
				return nil
			}
			cfg, ok := cfgs[s.Name]
			if !ok {
				// A project server: its config belongs to the repo, so the
				// token goes to the user's own file under the same name.
				cfg = agent.MCPConfig{Type: "http"}
			}
			if cfg.Headers == nil {
				cfg.Headers = map[string]string{}
			}
			cfg.Headers["Authorization"] = "Bearer " + tok
			cfgs[s.Name] = cfg
			if err := agent.SaveUserMCPConfig(configDir(), cfgs); err != nil {
				m.add(&block{kind: kindError, text: "MCP: " + err.Error()})
				m.refresh()
				return nil
			}
			m.note("token saved for "+s.Name+"; reconnecting", true)
			return m.retryMCP(eng, s.Name)
		})
}

// retryMCP reconnects one server and reports how it went.
func (m *Model) retryMCP(eng *agent.Engine, name string) tea.Cmd {
	return func() tea.Msg {
		if err := eng.ReconnectMCP(name); err != nil {
			return mcpDoneMsg{server: name, err: err}
		}
		return mcpDoneMsg{server: name}
	}
}

func (m *Model) handleMCPApproved(msg mcpApprovedMsg) tea.Cmd {
	if msg.err != nil {
		m.add(&block{kind: kindError, text: "MCP: " + msg.err.Error()})
		m.refresh()
		return nil
	}
	return m.showMCP()
}

func (m *Model) handleMCPDone(msg mcpDoneMsg) tea.Cmd {
	if msg.err != nil {
		m.note("MCP "+msg.server+": "+msg.err.Error(), false)
		return nil
	}
	m.note(msg.server+" reconnecting…", true)
	return nil
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
