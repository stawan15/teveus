package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// First-run setup: three short steps, each skippable with esc. The order puts
// the quick, reversible choices first and connecting (which may open a
// browser) last.

func (m *Model) startOnboarding() {
	cs := []choice{
		{label: "Guided", value: "guided", desc: "starter prompts, extra tips, plain explanations · new to AI coding tools"},
		{label: "Standard", value: "standard", desc: "starter prompts and shortcut tips · recommended"},
		{label: "Pro", value: "pro", desc: "compact layout, minimal hints · you know the keys"},
	}
	for i := range cs {
		level := cs[i].value
		cs[i].run = func(m *Model) tea.Cmd {
			m.settings.Level = level
			saveSettings(m.settings)
			m.refresh()
			m.onboardTheme()
			return nil
		}
	}
	m.openPicker("Welcome to teveus  ·  1/3  How much guidance would you like?", cs, 1)
	m.pop.flat = true
	m.pop.cancel = func(m *Model) { m.finishOnboarding() }
}

func (m *Model) onboardTheme() {
	m.openThemePicker()
	m.pop.title = "Welcome to teveus  ·  2/3  Pick a look (previews live)"
	orig := m.pop.cancel
	for i := range m.pop.all {
		run := m.pop.all[i].run
		m.pop.all[i].run = func(m *Model) tea.Cmd {
			cmd := run(m)
			m.onboardConnect()
			return cmd
		}
	}
	m.pop.filter()
	m.pop.cancel = func(m *Model) {
		orig(m)
		m.finishOnboarding()
	}
}

func (m *Model) onboardConnect() {
	sub := "runs your installed Claude Code · you sign in through Anthropic"
	if m.claudeAuth.loggedIn {
		sub = "✓ logged in as " + m.claudeAuth.email
	}
	cs := []choice{
		{label: "Claude Code (your Claude account)", desc: sub, run: func(m *Model) tea.Cmd {
			m.finishOnboarding()
			if m.engine == "claude" && m.claudeAuth.loggedIn {
				return nil
			}
			return m.useSubscription()
		}},
		{label: "Other AI providers", desc: "OpenRouter browser login, or an OpenAI / Gemini / Anthropic key, or a local model", run: func(m *Model) tea.Cmd {
			m.finishOnboarding()
			return m.openLogin("")
		}},
		{label: "Decide later", desc: "/login any time", run: func(m *Model) tea.Cmd {
			m.finishOnboarding()
			return nil
		}},
	}
	m.openPicker("Welcome to teveus  ·  3/3  How do you want to connect?", cs, 0)
	m.pop.flat = true
	m.pop.cancel = func(m *Model) { m.finishOnboarding() }
}

func (m *Model) finishOnboarding() {
	if m.settings.Onboarded {
		return
	}
	m.settings.Onboarded = true
	saveSettings(m.settings)
	m.note("all set · press ? any time for help", true)
	m.refresh()
}

// openHelp lists every shortcut, searchable by typing.
func (m *Model) openHelp() {
	type row struct{ group, key, what string }
	rows := []row{
		{"Basics", "enter", "send your message"},
		{"Basics", "shift+enter", "new line in the message (or ctrl+j)"},
		{"Basics", "ctrl+v", "paste an image (or drop an image file)"},
		{"Basics", "↑ ↓", "previous messages"},
		{"Basics", "esc esc", "stop the current task"},
		{"Basics", "ctrl+c ctrl+c", "quit"},
		{"Find anything", "ctrl+k", "command palette: every action"},
		{"Find anything", "/", "slash commands (e.g. /q to quit)"},
		{"Find anything", "@", "attach a file by name"},
		{"Find anything", "?  or F1", "this help"},
		{"Work", "shift+tab", "permission mode: ask first → auto-edit → plan only → autopilot"},
		{"Work", "!command", "run a shell command, e.g. !npm test"},
		{"Work", "/undo", "undo the last turn's file changes (Direct API)"},
		{"Work", "/diff", "show what changed"},
		{"Work", "/resume", "reopen an earlier conversation"},
		{"Work", "/init", "describe this project for the AI (AGENTS.md)"},
		{"Work", "/model", "switch model"},
		{"Work", "/login", "connect Claude or other providers"},
		{"Read & copy", "drag", "select text; release to copy"},
		{"Read & copy", "double / triple click", "select a word / a line"},
		{"Read & copy", "ctrl+y", "copy the last reply"},
		{"Read & copy", "click a tool", "open or close its output"},
		{"Read & copy", "ctrl+o", "open or close all tool output"},
		{"Read & copy", "pgup / pgdn / wheel", "scroll"},
		{"Look & feel", "/settings", "all preferences"},
		{"Look & feel", "/theme", "colours (incl. high contrast)"},
		{"Look & feel", "ctrl+b", "show or hide the sidebar"},
		{"Look & feel", "/export", "save the conversation as Markdown"},
	}
	var cs []choice
	for _, r := range rows {
		cs = append(cs, choice{group: r.group, label: r.key, desc: r.what})
	}
	m.openPicker("Keyboard & commands  ·  type to search", cs, 0)
}

// openSettings shows every preference with its current value; toggles apply
// immediately and the list reopens on the same row.
func (m *Model) openSettings() { m.openSettingsAt(0) }

func (m *Model) openSettingsAt(sel int) {
	levelNames := map[string]string{"guided": "Guided", "standard": "Standard", "pro": "Pro"}
	toggle := func(i int, f func(m *Model) tea.Cmd) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			cmd := f(m)
			m.openSettingsAt(i)
			return cmd
		}
	}
	items := []struct {
		group, label, value string
		run                 func(m *Model) tea.Cmd
	}{
		{"Experience", "Guidance level", levelNames[m.level()], func(m *Model) tea.Cmd { m.openLevelSlider(); return nil }},
		{"Experience", "Theme", theme.Name, func(m *Model) tea.Cmd { m.openThemePicker(); return nil }},
		{"Experience", "Reduce motion", onOff(m.settings.ReduceMotion), func(m *Model) tea.Cmd {
			m.settings.ReduceMotion = !m.settings.ReduceMotion
			saveSettings(m.settings)
			return nil
		}},
		{"Layout", "Sidebar", onOff(!m.settings.HideSide), func(m *Model) tea.Cmd { return m.toggleSidebar() }},
		{"Layout", "Tool output expanded", onOff(m.r.expand), func(m *Model) tea.Cmd { return m.toggleDetails() }},
		{"Layout", "Mouse (scroll, select, click)", onOff(!m.settings.NoMouse), func(m *Model) tea.Cmd { return m.toggleMouse() }},
		{"AI", "Engine", engineLabel(m.engine), func(m *Model) tea.Cmd { m.openEnginePicker(); return nil }},
		{"AI", "Model", orDefault(shortModel(m.model)), func(m *Model) tea.Cmd { m.openModelPicker(); return nil }},
		{"AI", "Reasoning effort", m.effort(), func(m *Model) tea.Cmd { m.openEffortSlider(); return nil }},
		{"AI", "Concise answers", onOff(m.settings.Concise), func(m *Model) tea.Cmd { return m.toggleConcise() }},
		{"AI", "Lean tools (Claude Code)", onOff(m.settings.LeanTools), func(m *Model) tea.Cmd { return m.toggleLean() }},
		{"AI", "Minimal code", m.minimalLevel(), func(m *Model) tea.Cmd { return m.setMinimal("") }},
		{"Setup", "Connect providers…", "", func(m *Model) tea.Cmd { return m.openLogin("") }},
		{"Setup", "Run first-time setup again", "", func(m *Model) tea.Cmd {
			m.settings.Onboarded = false
			m.startOnboarding()
			return nil
		}},
	}
	var cs []choice
	for i, it := range items {
		run := it.run
		switch it.label {
		case "Reduce motion", "Sidebar", "Tool output expanded", "Mouse (scroll, select, click)", "Concise answers", "Lean tools (Claude Code)":
			run = toggle(i, run) // on/off rows stay in the list so you can see the change
		}
		c := choice{group: it.group, label: it.label, key: it.value, run: run}
		if it.value == "on" {
			c.badge = "on"
			c.key = ""
		}
		cs = append(cs, c)
	}
	m.openPicker("Settings", cs, sel)
}

func orDefault(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

func (m *Model) openLevelSlider() {
	cs := []choice{
		{label: "Guided", value: "guided", desc: "starter prompts, extra tips"},
		{label: "Standard", value: "standard", desc: "starter prompts and shortcut tips"},
		{label: "Pro", value: "pro", desc: "compact, minimal hints"},
	}
	sel := 1
	for i := range cs {
		level := cs[i].value
		if level == m.level() {
			sel = i
		}
		cs[i].run = func(m *Model) tea.Cmd {
			m.settings.Level = level
			saveSettings(m.settings)
			for _, b := range m.blocks {
				b.invalidate()
			}
			m.refresh()
			m.note("guidance → "+level, true)
			return nil
		}
	}
	m.openSlider("Guidance level", "How much help teveus shows", cs, sel)
}
