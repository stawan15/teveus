package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

type localCmd struct {
	name, desc, key string
	run             func(m *Model, arg string) tea.Cmd
}

// Commands handled by this app rather than the CLI.
var localCmds []localCmd

// Filled in init: the commands refer back to the command list.
func init() {
	localCmds = []localCmd{
		{"clear", "Start a new conversation", "", func(m *Model, _ string) tea.Cmd { return m.clearConversation() }},
		{"model", "Switch model", "", func(m *Model, arg string) tea.Cmd {
			if arg != "" {
				m.setModel(arg)
			} else {
				m.openModelPicker()
			}
			return nil
		}},
		{"login", "Connect a provider: API key or browser login", "", func(m *Model, arg string) tea.Cmd { return m.openLogin(arg) }},
		{"logout", "Remove a saved provider key", "", func(m *Model, _ string) tea.Cmd { m.openLogout(); return nil }},
		{"engine", "Switch engine: Claude Code or direct API (your keys)", "", func(m *Model, arg string) tea.Cmd {
			if arg == "claude" || arg == "api" {
				return m.setEngine(arg)
			}
			m.openEnginePicker()
			return nil
		}},
		{"mode", "Choose permission mode", "shift+tab", func(m *Model, _ string) tea.Cmd { m.openModePicker(); return nil }},
		{"theme", "Change colour theme", "", func(m *Model, arg string) tea.Cmd {
			if t, ok := themeByName(arg); ok {
				m.setTheme(t)
				m.settings.Theme = t.Name
				saveSettings(m.settings)
			} else {
				m.openThemePicker()
			}
			return nil
		}},
		{"resume", "Resume an earlier conversation in this folder", "", func(m *Model, _ string) tea.Cmd { m.openResumePicker(); return nil }},
		{"undo", "Undo the last turn's file edits (Direct API)", "", func(m *Model, _ string) tea.Cmd { return m.undo() }},
		{"init", "Write AGENTS.md / CLAUDE.md describing this project", "", func(m *Model, _ string) tea.Cmd { return m.initProject() }},
		{"diff", "Show what changed in the working tree (git)", "", func(m *Model, _ string) tea.Cmd { return m.gitDiff() }},
		{"update", "Check for a new version of teveus and install it", "", func(m *Model, _ string) tea.Cmd { return m.updateNow() }},
		{"purge", "Delete teveus's saved conversations and prompt history", "", func(m *Model, _ string) tea.Cmd { m.openPurge(); return nil }},
		{"permissions", "Saved allow/deny rules for tools", "", func(m *Model, arg string) tea.Cmd { return m.showPermissions(arg) }},
		{"mcp", "MCP servers: list them, approve a project's", "", func(m *Model, _ string) tea.Cmd { return m.showMCP() }},
		{"export", "Save this conversation as Markdown", "", func(m *Model, _ string) tea.Cmd { return m.exportTranscript() }},
		{"copy", "Copy the last response", "ctrl+y", func(m *Model, _ string) tea.Cmd { return m.copyLast() }},
		{"sidebar", "Show or hide the sidebar", "ctrl+b", func(m *Model, _ string) tea.Cmd { return m.toggleSidebar() }},
		{"details", "Expand or collapse tool output", "ctrl+o", func(m *Model, _ string) tea.Cmd { return m.toggleDetails() }},
		{"concise", "Concise answers on/off (saves output tokens)", "", func(m *Model, _ string) tea.Cmd { return m.toggleConcise() }},
		{"lean", "Lean tools on/off (saves ~2.2k tokens per request)", "", func(m *Model, _ string) tea.Cmd { return m.toggleLean() }},
		{"effort", "Reasoning effort: how hard the model thinks (auto, low … max)", "", func(m *Model, arg string) tea.Cmd { return m.setEffort(arg) }},
		{"minimal", "Minimal code: how much code the model may write (off, lite, full, strict)", "", func(m *Model, arg string) tea.Cmd { return m.setMinimal(arg) }},
		{"trim", "Review uncommitted changes for unneeded code (/trim all: whole project)", "", func(m *Model, arg string) tea.Cmd { return m.trim(arg) }},
		{"mouse", "Mouse capture on/off (off = select text)", "", func(m *Model, _ string) tea.Cmd { return m.toggleMouse() }},
		{"help", "Keyboard shortcuts and commands (or press ?)", "?", func(m *Model, _ string) tea.Cmd { m.openHelp(); return nil }},
		{"settings", "All preferences in one place", "", func(m *Model, _ string) tea.Cmd { m.openSettings(); return nil }},
		{"setup", "Run the first-time setup again", "", func(m *Model, _ string) tea.Cmd { m.startOnboarding(); return nil }},
		{"exit", "Quit", "ctrl+c ×2", func(m *Model, _ string) tea.Cmd { return m.quit() }},
	}
}

func (m *Model) localCommand(name, arg string) (tea.Cmd, bool) {
	if full, ok := aliases[name]; ok {
		name = full
	}
	for _, c := range localCmds {
		if c.name == name {
			return c.run(m, arg), true
		}
	}
	return nil, false
}

func (m *Model) slashChoices() []choice {
	var out []choice
	seen := map[string]bool{}
	for _, c := range localCmds {
		c := c
		seen[c.name] = true
		key := c.key
		if as := aliasesOf(c.name); len(as) > 0 && key == "" {
			key = "/" + strings.Join(as, " /")
		}
		out = append(out, choice{label: "/" + c.name, value: c.name, desc: c.desc, key: key, aliases: aliasesOf(c.name),
			run: func(m *Model) tea.Cmd { return c.run(m, "") }})
	}
	for _, c := range m.commands {
		if seen[c.Name] || len(c.Name) > 1 && c.Name[:2] == "__" {
			continue
		}
		seen[c.Name] = true
		out = append(out, m.cliChoice(c))
	}
	return out
}

// cliChoice runs a CLI slash command. Commands with a free-form argument are
// completed into the input so the user can type it; enumerated arguments
// get a picker.
func (m *Model) cliChoice(c claude.Command) choice {
	desc := c.Description
	if c.ArgumentHint != "" {
		desc = c.ArgumentHint + "  " + desc
	}
	return choice{label: "/" + c.Name, value: c.Name, desc: desc, run: func(m *Model) tea.Cmd {
		if c.ArgumentHint != "" && parseEnum(c.ArgumentHint) == nil {
			m.input.SetValue("/" + c.Name + " ")
			m.afterInput()
			return nil
		}
		return m.submit("/" + c.Name)
	}}
}

func (m *Model) fileChoices() []choice {
	out := make([]choice, 0, len(m.files))
	for _, f := range m.files {
		if f != "" {
			out = append(out, choice{label: f, value: f})
		}
	}
	return out
}

func (m *Model) openPalette() {
	run := func(f func(m *Model) tea.Cmd) func(m *Model) tea.Cmd { return f }
	cs := []choice{
		{group: "session", label: "New conversation", desc: "clear the transcript and start fresh", key: "/clear", run: run(func(m *Model) tea.Cmd { return m.clearConversation() })},
		{group: "session", label: "Compact conversation", desc: "summarise history to free context", key: "/compact", run: run(func(m *Model) tea.Cmd { return m.send("/compact") })},
		{group: "session", label: "Resume a conversation…", desc: "earlier sessions in this folder", key: "/resume", run: run(func(m *Model) tea.Cmd { m.openResumePicker(); return nil })},
		{group: "session", label: "Undo last turn", desc: "restore files the last turn changed", key: "/undo", run: run(func(m *Model) tea.Cmd { return m.undo() })},
		{group: "session", label: "Show changes", desc: "git status and diff stat", key: "/diff", run: run(func(m *Model) tea.Cmd { return m.gitDiff() })},
		{group: "session", label: "Export conversation", desc: "save as Markdown in this folder", key: "/export", run: run(func(m *Model) tea.Cmd { return m.exportTranscript() })},
		{group: "session", label: "Initialise project", desc: "write AGENTS.md / CLAUDE.md", key: "/init", run: run(func(m *Model) tea.Cmd { return m.initProject() })},
		{group: "session", label: "Copy last response", desc: "to the system clipboard", key: "ctrl+y", run: run(func(m *Model) tea.Cmd { return m.copyLast() })},
		{group: "model", label: "Switch engine…", desc: engineLabel(m.engine) + " · or your own API keys", key: "/engine", run: run(func(m *Model) tea.Cmd { m.openEnginePicker(); return nil })},
		{group: "model", label: "Connect a provider…", desc: "API key or browser login", key: "/login", run: run(func(m *Model) tea.Cmd { return m.openLogin("") })},
		{group: "model", label: "Switch model…", desc: shortModel(m.model), key: "/model", run: run(func(m *Model) tea.Cmd { m.openModelPicker(); return nil })},
		{group: "model", label: "Reasoning effort…", desc: "how hard Claude thinks", key: "/effort", run: run(func(m *Model) tea.Cmd { return m.submit("/effort") })},
		{group: "model", label: "Permission mode…", desc: modeLabel(m.mode), key: "shift+tab", run: run(func(m *Model) tea.Cmd { m.openModePicker(); return nil })},
		{group: "usage", label: "Concise answers", desc: onOff(m.settings.Concise) + " · short, direct replies", key: "/concise", run: run(func(m *Model) tea.Cmd { return m.toggleConcise() })},
		{group: "usage", label: "Lean tools", desc: onOff(m.settings.LeanTools) + " · ~2.2k fewer tokens per request", key: "/lean", run: run(func(m *Model) tea.Cmd { return m.toggleLean() })},
		{group: "model", label: "Reasoning effort", desc: m.effort() + " · how hard the model thinks", key: "/effort", run: run(func(m *Model) tea.Cmd { m.openEffortSlider(); return nil })},
		{group: "usage", label: "Minimal code", desc: m.minimalLevel() + " · write only what the task needs", key: "/minimal", run: run(func(m *Model) tea.Cmd { return m.setMinimal("") })},
		{group: "view", label: "Theme…", desc: theme.Name, key: "/theme", run: run(func(m *Model) tea.Cmd { m.openThemePicker(); return nil })},
		{group: "view", label: "Toggle sidebar", desc: onOff(!m.settings.HideSide), key: "ctrl+b", run: run(func(m *Model) tea.Cmd { return m.toggleSidebar() })},
		{group: "view", label: "Toggle tool details", desc: onOff(m.r.expand), key: "ctrl+o", run: run(func(m *Model) tea.Cmd { return m.toggleDetails() })},
		{group: "view", label: "Toggle mouse capture", desc: onOff(!m.settings.NoMouse) + " · off lets you select text", key: "/mouse", run: run(func(m *Model) tea.Cmd { return m.toggleMouse() })},
		{group: "app", label: "Settings…", desc: "theme, guidance, layout, AI", key: "/settings", run: run(func(m *Model) tea.Cmd { m.openSettings(); return nil })},
		{group: "app", label: "Keyboard shortcuts", key: "?", run: run(func(m *Model) tea.Cmd { m.openHelp(); return nil })},
		{group: "app", label: "Quit", key: "ctrl+c ×2", run: run(func(m *Model) tea.Cmd { return m.quit() })},
	}
	for _, c := range m.commands {
		if len(c.Name) > 1 && c.Name[:2] == "__" {
			continue
		}
		ch := m.cliChoice(c)
		ch.group = "claude commands"
		cs = append(cs, ch)
	}
	m.pop = popup{mode: popPalette, all: cs}
	m.pop.filter()
	m.layout()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m *Model) openPicker(title string, cs []choice, sel int) {
	m.pop = popup{mode: popPicker, title: title, all: cs}
	m.pop.filter()
	m.pop.sel = max(0, min(sel, len(m.pop.list)-1))
	m.layout()
}

func (m *Model) openModelPicker() {
	if len(m.models) == 0 {
		if m.engine != "claude" {
			// Nothing connected: offer the two ways forward instead of a dead end.
			sub := "runs your installed Claude Code · you sign in through Anthropic"
			if m.claudeAuth.loggedIn {
				sub = "✓ " + m.claudeAuth.email + " · Opus, Sonnet, Haiku"
			}
			title := "Model · no API provider connected"
			if m.engine == "" {
				title = "Model · connect an AI first"
			}
			m.openPicker(title, []choice{
				{label: "Use Claude Code", desc: sub, run: func(m *Model) tea.Cmd { return m.useSubscription() }},
				{label: "Connect a provider…", desc: "OpenRouter browser login, or an OpenAI / Gemini / Anthropic key", run: func(m *Model) tea.Cmd { return m.openLogin("") }},
			}, 0)
		} else {
			m.note("model list is still loading, or use /model <name>", false)
		}
		return
	}
	pick := func(v string) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd { m.setModel(v); return nil }
	}
	byValue := map[string]claude.ModelInfo{}
	for _, mi := range m.models {
		byValue[mi.Value] = mi
	}
	mk := func(mi claude.ModelInfo, group string) choice {
		c := choice{label: mi.DisplayName, value: mi.Value, desc: mi.Description, key: mi.Meta, group: group, run: pick(mi.Value)}
		if c.label == "" {
			c.label = mi.Value
		}
		if mi.Free {
			c.badge = "free"
		}
		return c
	}
	// With a single provider its name goes in the title, not on every group.
	providers := map[string]bool{}
	for _, mi := range m.models {
		p, _, _ := strings.Cut(mi.Group, " · ")
		providers[p] = true
	}
	title := "Model"
	single := ""
	if len(providers) == 1 {
		for p := range providers {
			single = p
		}
		title += " · " + single
	}
	short := func(g string) string {
		if single != "" {
			if _, v, ok := strings.Cut(g, " · "); ok {
				return v
			}
		}
		return g
	}

	var cs []choice
	if m.engine == "api" {
		// Opus and friends on a Pro/Max plan run through the Claude Code engine.
		desc := "runs your installed Claude Code · you sign in through Anthropic"
		if m.claudeAuth.loggedIn {
			desc = "✓ " + m.claudeAuth.email + " · switch to the Claude Code engine"
		}
		cs = append(cs, choice{label: "Use Claude Code", value: "engine:claude", desc: desc,
			group: "Claude Code", run: func(m *Model) tea.Cmd { return m.useSubscription() }})
	}
	// Recently used models first: most people switch between a handful.
	for _, v := range m.settings.RecentModels {
		if mi, ok := byValue[v]; ok && m.engine == "api" {
			c := mk(mi, "Recent")
			c.desc = strings.TrimSpace(mi.Description + "  " + short(mi.Group))
			cs = append(cs, c)
		}
	}
	// Free models next: handy on a small balance.
	for _, mi := range m.models {
		if mi.Free && m.engine == "api" {
			cs = append(cs, mk(mi, "Free"))
		}
	}
	for _, mi := range m.models {
		cs = append(cs, mk(mi, short(mi.Group)))
	}
	// Default to the first real model, never the engine switch above it.
	sel := 0
	if len(cs) > 1 && strings.HasPrefix(cs[0].value, "engine:") {
		sel = 1
	}
	for i, c := range cs {
		if c.value == m.model || shortModel(m.model) == c.value || strings.HasSuffix(c.value, "/"+m.model) {
			sel = i
			break
		}
	}
	m.openPicker(title, cs, sel)
}

func (m *Model) openModePicker() {
	var cs []choice
	sel := 0
	for i, md := range modes {
		md := md
		if md == m.mode {
			sel = i
		}
		cs = append(cs, choice{label: modeLabel(md), desc: modeHelp(md, m.engine), run: func(m *Model) tea.Cmd { m.setMode(md); return nil }})
	}
	m.openPicker("Permission mode", cs, sel)
}

// openThemePicker previews each theme live as you move through the list;
// esc restores the one you had.
func (m *Model) openThemePicker() {
	orig := theme
	var cs []choice
	sel := 0
	for i, t := range themes {
		t := t
		if t.Name == orig.Name {
			sel = i
		}
		kind := "dark"
		if !t.Dark {
			kind = "light"
		}
		cs = append(cs, choice{label: t.Name, value: t.Name, desc: kind, run: func(m *Model) tea.Cmd {
			m.setTheme(t)
			m.settings.Theme = t.Name
			saveSettings(m.settings)
			m.note("theme → "+t.Name, true)
			return nil
		}})
	}
	m.openPicker("Theme", cs, sel)
	m.pop.preview = func(m *Model, c choice) {
		if t, ok := themeByName(c.value); ok {
			m.setTheme(t)
		}
	}
	m.pop.cancel = func(m *Model) { m.setTheme(orig) }
}

// openArgPicker turns "/effort" into a list of its allowed values.
func (m *Model) openArgPicker(c claude.Command, opts []string) {
	var cs []choice
	for _, o := range opts {
		o := o
		cs = append(cs, choice{label: o, run: func(m *Model) tea.Cmd { return m.send(fmt.Sprintf("/%s %s", c.Name, o)) }})
	}
	if isScale(opts) {
		// Ordered levels (/effort low|medium|high…) read better on a slider.
		m.openSlider("/"+c.Name, c.Description, cs, len(cs)/2)
		return
	}
	m.openPicker("/"+c.Name+"  "+c.Description, cs, 0)
}
