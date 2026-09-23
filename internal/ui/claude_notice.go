package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

// Before Claude Code first runs, the user confirms that teveus is starting
// their own installed Claude Code, which they sign in to with Anthropic and
// use under Anthropic's terms. This is what Anthropic's terms allow ("an
// end user signing in to the unmodified Claude Code binary with their own
// Claude subscription"); the confirmation makes it the user's choice, once.

const claudeNotice = "teveus will run Claude Code, which you installed from Anthropic. You sign in to it with Anthropic, teveus never sees your login, " +
	"and your use is covered by Anthropic's terms (anthropic.com/legal). teveus is independent: it isn't made or endorsed by Anthropic."

// claudeNoticeRead is how long "Continue" waits, so the notice is read
// rather than pressed through.
const claudeNoticeRead = 4 * time.Second

// claudeNoticeBody is the notice as four short points, key words in bold.
func claudeNoticeBody() []string {
	b := func(s string) string { return sText.Bold(true).Render(s) }
	d := sDim.Render
	dot := sAccent.Render("  • ")
	return []string{
		"",
		dot + d("teveus runs ") + b("your own Claude Code") + d(", installed by you from Anthropic"),
		dot + d("You sign in with ") + b("Anthropic") + d(": teveus ") + b("never sees your login"),
		dot + d("Your use is covered by ") + b("Anthropic's terms") + d(" · anthropic.com/legal"),
		dot + d("teveus is ") + b("independent") + d(": not made or endorsed by Anthropic"),
	}
}

func (m *Model) claudeConfirmed() bool { return m.settings.ClaudeNotice || m.cfg.Headless }

// askClaudeNotice asks once; then runs after the user agrees.
func (m *Model) askClaudeNotice(then func(m *Model) tea.Cmd) {
	m.openPicker("Use Claude Code?", []choice{
		{label: "Continue with Claude Code", value: "accept", desc: "I've read this · don't ask again", run: func(m *Model) tea.Cmd {
			m.settings.ClaudeNotice = true
			saveSettings(m.settings)
			return then(m)
		}},
		{label: "Use my own API keys instead", desc: "OpenRouter, OpenAI, Gemini, Anthropic API or a local model", run: func(m *Model) tea.Cmd {
			cmd := m.setEngine("api")
			return tea.Batch(cmd, m.openLogin(""))
		}},
		{label: "Not now", desc: "Claude Code stays off · /engine to choose later", run: func(m *Model) tea.Cmd {
			m.note("Claude Code not started · /engine to choose", false)
			return nil
		}},
	}, 2) // start on "Not now": agreeing takes a deliberate move
	m.pop.body = claudeNoticeBody()
	m.pop.hold, m.pop.holdEnd = "accept", time.Now().Add(claudeNoticeRead)
	m.pop.flat = true
	m.pop.cancel = func(m *Model) { m.note("Claude Code not started · /engine to choose", false) }
}

// gateClaude holds back starting Claude Code until the user has confirmed.
// It reports whether the start should wait.
func (m *Model) gateClaude(opts claude.Options) bool {
	if m.engine != "claude" || m.claudeConfirmed() {
		return false
	}
	// First-run setup asks in its own last step.
	if m.cfg.Onboard && !m.settings.Onboarded {
		return true
	}
	if !m.pop.open() {
		m.askClaudeNotice(func(m *Model) tea.Cmd { return m.start(opts) })
	}
	return true
}
