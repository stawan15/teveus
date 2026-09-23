package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

type loginResultMsg struct {
	claude bool // the Claude subscription login finished
	p      agent.Provider
	cred   agent.Credential
	n      int
	err    error
	save   bool
}

// claudeAuthMsg reports whether the claude CLI is logged in to a Claude
// subscription; that login is what the Claude Code engine uses.
type claudeAuthMsg struct {
	loggedIn bool
	method   string
	email    string
}

func checkClaudeAuth(bin string) tea.Cmd {
	return func() tea.Msg {
		if bin == "" {
			bin = "claude"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "auth", "status").Output()
		var st struct {
			LoggedIn   bool   `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
			Email      string `json:"email"`
		}
		if err != nil || json.Unmarshal(out, &st) != nil {
			return claudeAuthMsg{}
		}
		return claudeAuthMsg{loggedIn: st.LoggedIn, method: st.AuthMethod, email: st.Email}
	}
}

// useSubscription switches to the Claude Code engine, running Claude Code's
// own sign-in first if needed. Anthropic doesn't let third-party apps offer
// Claude.ai login or touch Claude.ai credentials, but a user may sign in to
// the unmodified Claude Code with their own plan; that is all teveus does.
// See "Anthropic's terms" in CLAUDE.md before changing anything here.
func (m *Model) useSubscription() tea.Cmd {
	if !m.claudeConfirmed() {
		m.askClaudeNotice(func(m *Model) tea.Cmd { return m.useSubscription() })
		return nil
	}
	if m.claudeAuth.loggedIn {
		cmd := m.setEngine("claude")
		m.note("using Claude Code, signed in as "+m.claudeAuth.email+" · /model to pick Opus", true)
		return cmd
	}
	bin := m.cfg.Claude.Binary
	if bin == "" {
		bin = "claude"
	}
	m.note("opening Anthropic's Claude Code sign-in…", true)
	return tea.ExecProcess(exec.Command(bin, "auth", "login"), func(err error) tea.Msg {
		if err != nil {
			return loginResultMsg{err: fmt.Errorf("claude login: %w", err), claude: true}
		}
		return loginResultMsg{claude: true}
	})
}

// hasAPICredentials reports whether any API provider has a saved or
// environment key (local servers don't count: they may not be running).
func hasAPICredentials() bool {
	s := agent.NewStore(configDir())
	for _, p := range agent.Providers {
		if _, src, _ := s.Lookup(p); src == agent.FromStore || src == agent.FromEnv {
			return true
		}
	}
	return false
}

func (m *Model) store() *agent.Store { return agent.NewStore(configDir()) }

func providerStatus(s *agent.Store, p agent.Provider) (string, bool) {
	_, src, env := s.Lookup(p)
	switch src {
	case agent.FromStore:
		return "✓ connected", true
	case agent.FromEnv:
		return "✓ from $" + env, true
	case agent.FromLocal:
		return "local server, no key needed", false
	}
	switch {
	case p.Browser:
		return "browser login or API key", false
	case p.Custom:
		return "any OpenAI-compatible endpoint", false
	}
	return "API key", false
}

// openLogin lists providers with their status; "/login openai" jumps
// straight to one.
func (m *Model) openLogin(arg string) tea.Cmd {
	if p, ok := agent.ProviderByID(strings.ToLower(arg)); ok {
		return m.loginProvider(p)
	}
	for _, p := range agent.SearchProviders {
		if p.ID == strings.ToLower(arg) {
			m.keyInput(p)
			return nil
		}
	}
	s := m.store()
	sub := "runs your installed Claude Code · you sign in through Anthropic"
	subLabel := "Claude Code (your Claude account)"
	if m.claudeAuth.loggedIn {
		sub = "✓ logged in as " + m.claudeAuth.email + " · uses the Claude Code engine"
		subLabel = "● " + subLabel
	}
	cs := []choice{{label: subLabel, value: "claude", desc: sub,
		run: func(m *Model) tea.Cmd { return m.useSubscription() }}}
	for _, p := range agent.Providers {
		p := p
		status, ok := providerStatus(s, p)
		label := p.Name
		if ok {
			label = "● " + label
		}
		cs = append(cs, choice{label: label, value: p.ID, desc: status,
			run: func(m *Model) tea.Cmd { return m.loginProvider(p) }})
	}
	for _, p := range agent.SearchProviders {
		p := p
		status, ok := providerStatus(s, p)
		label := p.Name
		if ok {
			label = "● " + label
		}
		cs = append(cs, choice{label: label, value: p.ID, desc: status + " · web search for the Direct API engine", group: "Web search",
			run: func(m *Model) tea.Cmd { m.keyInput(p); return nil }})
	}
	m.openPicker("Connect a provider", cs, 0)
	return nil
}

func (m *Model) loginProvider(p agent.Provider) tea.Cmd {
	switch {
	case p.NoKey:
		m.note("checking "+p.Name+"…", true)
		return verify(p, agent.Credential{}, false)
	case p.Custom:
		m.openInput("Custom provider: base URL", "OpenAI-compatible API root, e.g. https://api.together.xyz/v1", false,
			func(m *Model, base string) tea.Cmd {
				base = strings.TrimRight(strings.TrimSpace(base), "/")
				if base == "" {
					return nil
				}
				m.openInput("Custom provider: API key", "Leave empty if the endpoint needs no key. Stored only on this machine.", true,
					func(m *Model, key string) tea.Cmd {
						m.note("checking "+base+"…", true)
						return verify(p, agent.Credential{Key: strings.TrimSpace(key), BaseURL: base}, true)
					})
				return nil
			})
		return nil
	case p.Browser:
		m.openPicker(p.Name, []choice{
			{label: "Log in with browser", desc: "recommended: approve in the browser, no copy-paste",
				run: func(m *Model) tea.Cmd { return m.browserLogin(p) }},
			{label: "Paste an API key", desc: p.KeyURL,
				run: func(m *Model) tea.Cmd { m.keyInput(p); return nil }},
		}, 0)
		return nil
	}
	m.keyInput(p)
	return nil
}

func (m *Model) keyInput(p agent.Provider) {
	hint := "Stored in ~/.config/teveus/auth.json, readable only by you."
	if p.KeyURL != "" {
		hint = "Create one at " + p.KeyURL + " · " + hint
	}
	m.openInput(p.Name+" API key", hint, true, func(m *Model, key string) tea.Cmd {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		m.note("checking key…", true)
		return verify(p, agent.Credential{Key: key}, true)
	})
}

func (m *Model) browserLogin(p agent.Provider) tea.Cmd {
	m.note("finish logging in in your browser…", true)
	return func() tea.Msg {
		key, err := agent.OpenRouterLogin(context.Background(), agent.OpenBrowser)
		if err != nil {
			return loginResultMsg{p: p, err: err}
		}
		cred := agent.Credential{Key: key}
		n, err := agent.Verify(context.Background(), p, cred)
		return loginResultMsg{p: p, cred: cred, n: n, err: err, save: true}
	}
}

// verify checks a credential against the provider before anything is saved.
func verify(p agent.Provider, cred agent.Credential, save bool) tea.Cmd {
	return func() tea.Msg {
		n, err := agent.Verify(context.Background(), p, cred)
		return loginResultMsg{p: p, cred: cred, n: n, err: err, save: save}
	}
}

func (m *Model) handleLogin(r loginResultMsg) tea.Cmd {
	if r.claude {
		if r.err != nil {
			m.add(&block{kind: kindError, text: r.err.Error()})
			m.refresh()
			return nil
		}
		m.claudeAuth.loggedIn = true
		return tea.Batch(checkClaudeAuth(m.cfg.Claude.Binary), m.setEngine("claude"))
	}
	if r.err != nil {
		m.add(&block{kind: kindError, text: fmt.Sprintf("Couldn't connect to %s: %v", r.p.Name, r.err)})
		m.refresh()
		return nil
	}
	if r.save {
		if err := m.store().Save(r.p.ID, r.cred); err != nil {
			m.add(&block{kind: kindError, text: "saving credentials: " + err.Error()})
			m.refresh()
			return nil
		}
	}
	if r.p.IsSearch() {
		m.note("✓ "+r.p.Name+" connected · the Direct API engine can search the web", true)
		if eng, ok := m.client.(*agent.Engine); ok {
			eng.Reload()
		}
		return nil
	}
	m.note(fmt.Sprintf("✓ %s connected · %d models", r.p.Name, r.n), true)
	return m.providersChanged()
}

// providersChanged refreshes the API engine's models, switching to it if
// the user just connected their first provider from the Claude engine.
func (m *Model) providersChanged() tea.Cmd {
	if m.engine != "api" {
		return m.setEngine("api")
	}
	if eng, ok := m.client.(*agent.Engine); ok {
		m.promptedModel = false
		eng.Reload()
	}
	return nil
}

func (m *Model) openLogout() {
	s := m.store()
	var cs []choice
	for _, p := range agent.Providers {
		p := p
		if _, src, _ := s.Lookup(p); src != agent.FromStore {
			continue
		}
		cs = append(cs, choice{label: p.Name, value: p.ID, desc: "remove saved key", run: func(m *Model) tea.Cmd {
			// Removing a login is easy to do by accident and tedious to
			// redo, so confirm, with "keep" as the default.
			m.openPicker("Remove the saved "+p.Name+" login?", []choice{
				{label: "Keep it", desc: "go back", run: func(m *Model) tea.Cmd { return nil }},
				{label: "Remove", desc: "you'll need /login again to use " + p.Name, run: func(m *Model) tea.Cmd {
					s.Delete(p.ID)
					m.note(p.Name+" login removed", true)
					return m.providersChanged()
				}},
			}, 0)
			return nil
		}})
	}
	if len(cs) == 0 {
		m.note("no saved keys (keys from environment variables can't be removed here)", false)
		return
	}
	m.openPicker("Log out", cs, 0)
}

// openInput shows a one-line text field in the popup, masked for secrets.
func (m *Model) openInput(title, hint string, masked bool, submit func(m *Model, v string) tea.Cmd) {
	m.pop = popup{mode: popInput, title: title, hint: hint, masked: masked, submit: submit}
	m.layout()
}
