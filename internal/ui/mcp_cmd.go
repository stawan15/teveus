package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

type mcpApprovedMsg struct{ err error }

// mcpDoneMsg carries the result of adding or retrying a server.
type mcpDoneMsg struct {
	server      string
	err         error
	switchedOn  bool
	switchedOff bool
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
	m.reportMCP(st)
	m.openPicker("MCP servers", m.mcpChoices(eng, st), 0)
	return nil
}

// reportMCP writes the server list into the transcript, replacing the last one
// so opening /mcp repeatedly doesn't stack identical panels.
func (m *Model) reportMCP(st []agent.MCPServerStatus) {
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
	// One summary, not one per run: /mcp is a panel, and opening it five times
	// used to print the same list five times into the transcript.
	if m.mcpBlock != nil {
		m.removeBlock(m.mcpBlock)
	}
	m.mcpBlock = &block{kind: kindCmdOut, text: sb.String()}
	m.add(m.mcpBlock)
	m.refresh()
}

// mcpLine is one server as a list row: its state, what it runs, and its tools.
func mcpLine(s agent.MCPServerStatus) string {
	mark := "✗"
	switch {
	case s.Disabled:
		mark = "○"
	case s.State == "connected":
		mark = "●"
	case s.State == "needs approval":
		mark = "◌"
	case s.State == "starting":
		mark = "…"
	}
	// Two lines per server: the name and how it stands, then what it runs.
	// One long line wrapped into the state half-way through, which made the
	// list hard to scan and pushed the target under the error text.
	line := fmt.Sprintf("- %s **%s**", mark, s.Name)
	if s.Scope != "" {
		line += " (" + s.Scope + ")"
	}
	line += " · " + shortMCPState(s.State)
	if len(s.Tools) > 0 {
		line += fmt.Sprintf(" · %d tools", len(s.Tools))
	}
	// A markdown hard break (two spaces) keeps these on their own lines;
	// markdown joins a plain newline into the same paragraph, which is what
	// put the target in the middle of the error text.
	if s.Target != "" {
		line += "  \n  `" + s.Target + "`"
	}
	if len(s.Tools) > 0 {
		line += "  \n  " + strings.Join(s.Tools, ", ")
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

// mcpChoices builds the list /mcp shows: every server with its state and an
// on/off switch, then what only some servers need (sign in, approve, retry),
// then the two housekeeping entries.
func (m *Model) mcpChoices(eng *agent.Engine, st []agent.MCPServerStatus) []choice {
	var cs []choice
	for _, s := range st {
		s := s
		// One row per server, in the same order as the panel above it.
		cs = append(cs, choice{
			label: mcpSwitch(s) + " " + s.Name,
			desc:  mcpRow(s),
			run:   func(m *Model) tea.Cmd { return m.toggleMCP(eng, s) },
		})
	}
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
		case s.NeedsOAuth:
			cs = append(cs, choice{
				label: "Sign in to " + s.Name,
				desc:  "opens your browser: this server uses OAuth, so a pasted token won't do",
				run: func(m *Model) tea.Cmd {
					m.note("opening the browser to sign in to "+s.Name+"…", true)
					return func() tea.Msg {
						return mcpDoneMsg{server: s.Name,
							err: eng.MCPLoginOAuth(context.Background(), s.Name, agent.OpenBrowserFor)}
					}
				},
			})
		case needsAuth(s.State) && !s.Disabled:
			cs = append(cs, choice{
				label: "Add a token for " + s.Name,
				desc:  "the server answered " + authCode(s.State) + "; the token goes in its \"headers\"",
				run: func(m *Model) tea.Cmd {
					m.askMCPToken(eng, s)
					return nil
				},
			})
		case !s.Disabled && s.State != "connected" && s.Scope == "project":
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

// mcpSwitch is the on/off mark in front of a server: on is filled and coloured,
// off is hollow and faint, so the column reads at a glance.
func mcpSwitch(s agent.MCPServerStatus) string {
	switch {
	case s.Disabled:
		return sFaint.Render("○")
	case s.State == "connected":
		return sGreen.Render("●")
	case s.State == "starting":
		return sAccent.Render("◐")
	case s.State == "needs approval":
		return sYellow.Render("◌")
	default:
		return sRed.Render("✗")
	}
}

// mcpRow is a server's one-line description in the list: how many tools it
// adds and where it points.
func mcpRow(s agent.MCPServerStatus) string {
	switch {
	case s.Disabled:
		out := "off · its tools aren't offered"
		if len(s.Tools) > 0 {
			out += fmt.Sprintf(" · %d tools when on", len(s.Tools))
		}
		return out
	case s.State == "connected":
		return fmt.Sprintf("%d tools · %s", len(s.Tools), s.Target)
	}
	return shortMCPState(s.State)
}

// toggleMCP flips one server on or off and says how it went.
func (m *Model) toggleMCP(eng *agent.Engine, s agent.MCPServerStatus) tea.Cmd {
	return func() tea.Msg {
		if err := eng.SetMCPEnabled(s.Name, s.Disabled); err != nil {
			return mcpDoneMsg{server: s.Name, err: err}
		}
		if s.Disabled {
			return mcpDoneMsg{server: s.Name, switchedOn: true}
		}
		return mcpDoneMsg{server: s.Name, switchedOff: true}
	}
}

// needsAuth reports a server that refused the connection for lack of a token.
func needsAuth(state string) bool {
	s := strings.ToLower(state)
	return strings.Contains(s, "401") || strings.Contains(s, "403") ||
		strings.Contains(s, "needs authorization") || strings.Contains(s, "unauthorized")
}

// authCode is the number a server answered with, which is what the user needs
// to look up: the state begins with the call ("initialize: 401 …").
func authCode(state string) string {
	for _, code := range []string{"401", "403"} {
		if strings.Contains(state, code) {
			return code
		}
	}
	return "an authorization error"
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
	switch {
	case msg.switchedOff:
		m.note(msg.server+" off: its tools aren't offered", true)
	case msg.switchedOn:
		m.note(msg.server+" on, starting…", true)
	default:
		m.note(msg.server+" reconnecting…", true)
	}
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
