package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// concisePrompt cut Opus output by ~60% on a typical question in testing,
// without dropping content. Override it with "stylePrompt" in settings.json.
const concisePrompt = `Answer style: concise and direct.
- Give the answer first, in plain words. Aim for under 120 words unless the user asks for detail or the task truly needs more.
- No preamble, no restating the question, no headings, no tables, no closing summary, no "let me know" offers.
- Code only when it is the answer or the user asks; keep it to the few lines that matter.
- When you finish a coding task, report what changed and the result in 1-3 sentences.
- Reply in the language the user writes in.`

// leanDisallowed are built-in tools this app rarely needs. Leaving them out
// trims ~2.2k tokens from every request. AskUserQuestion is also one the
// app cannot render, so Claude asks in plain text instead.
var leanDisallowed = []string{
	"NotebookEdit", "CronCreate", "CronDelete", "CronList", "RemoteTrigger",
	"ScheduleWakeup", "PushNotification", "DesignSync", "EnterWorktree",
	"ExitWorktree", "Monitor", "ListAgents", "SendMessage", "ReportFindings",
	"AskUserQuestion", "EnterPlanMode", "LSP",
}

// contextWarn is when a gentle /compact nudge appears: every message resends
// the whole conversation, so long sessions are what really burn usage.
const contextWarn = 100_000

func (m *Model) applySavers(opts claude.Options) claude.Options {
	if m.cfg.Full {
		return opts
	}
	var prompts []string
	if m.settings.Concise {
		p := concisePrompt
		if m.settings.StylePrompt != "" {
			p = m.settings.StylePrompt
		}
		prompts = append(prompts, p)
	}
	if p := minimalPrompt(m.minimalLevel()); p != "" {
		prompts = append(prompts, p)
	}
	opts.AppendPrompt = strings.Join(prompts, "\n\n")
	if m.settings.LeanTools && m.engine == "claude" {
		opts.DisallowTools = leanDisallowed
	}
	return opts
}

func (m *Model) saverLabel() string {
	if m.cfg.Full {
		return "off (-full)"
	}
	var on []string
	if m.settings.Concise {
		on = append(on, "concise")
	}
	if m.settings.LeanTools && m.engine == "claude" {
		on = append(on, "lean")
	}
	switch lvl := m.minimalLevel(); lvl {
	case "off":
	case "full":
		on = append(on, "minimal")
	default:
		on = append(on, "minimal "+lvl)
	}
	if len(on) == 0 {
		return "off"
	}
	s := on[0]
	for _, x := range on[1:] {
		s += " · " + x
	}
	return s
}

// restart relaunches the CLI with the current settings, keeping the
// conversation, since prompt and tool flags only apply at startup.
func (m *Model) restart(why string) tea.Cmd {
	if m.busy {
		m.note("finish or interrupt the current turn first", false)
		return nil
	}
	if m.client != nil {
		m.client.Close()
	}
	opts := m.cfg.Claude
	opts.Resume, opts.Continue = m.sessionID, m.sessionID == "" && opts.Continue
	cmd := m.start(opts)
	m.note(why, true)
	return cmd
}

// setEngine switches between Claude Code and the direct API engine.
// Conversations don't carry across engines, so this starts a new one.
func (m *Model) setEngine(engine string) tea.Cmd {
	if engine == m.engine {
		return nil
	}
	if engine == "claude" && !m.claudeConfirmed() {
		// Connecting Claude Code is when its notice is shown.
		m.askClaudeNotice(func(m *Model) tea.Cmd { return m.setEngine("claude") })
		return nil
	}
	if m.busy {
		m.note("finish or interrupt the current turn first", false)
		return nil
	}
	m.engine = engine
	m.settings.Engine = engine
	saveSettings(m.settings)
	m.model, m.commands, m.models = m.cfg.Claude.Model, nil, nil
	if engine == "api" && m.model == "" {
		m.model = m.settings.APIModel
	}
	m.fiveHour, m.sevenDay = nil, nil
	cmd := m.clearConversation()
	m.note("engine → "+engineLabel(engine)+" (new conversation)", true)
	return cmd
}

// agentName is how the assistant is referred to in notifications.
func (m *Model) agentName() string {
	if m.engine == "api" && m.model != "" {
		return shortModel(m.model)
	}
	return "Claude"
}

func engineLabel(e string) string {
	switch e {
	case "api":
		return "Direct API"
	case "claude":
		return "Claude Code"
	}
	return "not connected"
}

func (m *Model) openEnginePicker() {
	cs := []choice{
		{label: "Claude Code", value: "claude", desc: "Anthropic models via your claude CLI login (subscription)",
			run: func(m *Model) tea.Cmd { return m.setEngine("claude") }},
		{label: "Direct API", value: "api", desc: "your own keys: OpenAI, Anthropic, Gemini, OpenRouter, Groq, local models…",
			run: func(m *Model) tea.Cmd { return m.setEngine("api") }},
	}
	sel := 0
	if m.engine == "api" {
		sel = 1
	}
	m.openPicker("Engine", cs, sel)
}

func (m *Model) toggleConcise() tea.Cmd {
	m.settings.Concise = !m.settings.Concise
	saveSettings(m.settings)
	if eng, ok := m.client.(*agent.Engine); ok {
		// The API engine applies it from the next request; no restart needed.
		eng.SetStyle(m.applySavers(claude.Options{}).AppendPrompt)
		m.note("concise answers "+onOff(m.settings.Concise), true)
		return nil
	}
	if !m.settings.Concise {
		return m.restart("concise answers off")
	}
	return m.restart("concise answers on")
}

func (m *Model) toggleLean() tea.Cmd {
	m.settings.LeanTools = !m.settings.LeanTools
	saveSettings(m.settings)
	if !m.settings.LeanTools {
		return m.restart("all tools loaded")
	}
	return m.restart("lean tools on (~2.2k fewer tokens per request)")
}
