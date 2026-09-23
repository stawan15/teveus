package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	d := sText.Render
	point := func(c lipgloss.Color, parts ...string) string {
		// Odd parts are the key words, in the point's colour.
		kw := lipgloss.NewStyle().Foreground(c).Bold(true)
		s := lipgloss.NewStyle().Foreground(c).Render("  ◆ ")
		for i, p := range parts {
			if i%2 == 1 {
				s += kw.Render(p)
			} else {
				s += d(p)
			}
		}
		return s
	}
	return []string{
		"",
		point(cBlue, "teveus runs ", "your own Claude Code", ", installed by you from Anthropic"),
		point(cGreen, "You sign in with Anthropic: teveus ", "never sees your login"),
		point(cYellow, "Your use is covered by ", "Anthropic's terms", " · anthropic.com/legal"),
		point(cAccent2, "teveus is ", "independent", ": not made or endorsed by Anthropic"),
	}
}

func (m *Model) claudeConfirmed() bool { return m.settings.ClaudeNotice || m.cfg.Headless }

// askClaudeNotice asks once; then runs after the user agrees.
func (m *Model) askClaudeNotice(then func(m *Model) tea.Cmd) {
	m.openPicker("Use Claude Code?", []choice{
		{label: "Continue with Claude Code", value: "accept", color: cGreen, desc: "I've read this · don't ask again", run: func(m *Model) tea.Cmd {
			m.settings.ClaudeNotice = true
			saveSettings(m.settings)
			return then(m)
		}},
		{label: "Use my own API keys instead", color: cBlue, desc: "OpenRouter, OpenAI, Gemini, Anthropic API or a local model", run: func(m *Model) tea.Cmd {
			cmd := m.setEngine("api")
			return tea.Batch(cmd, m.openLogin(""))
		}},
		{label: "Not now", color: cDim, desc: "Claude Code stays off · /engine to choose later", run: func(m *Model) tea.Cmd {
			m.note("Claude Code not started · /engine to choose", false)
			return nil
		}},
	}, 2) // start on "Not now": agreeing takes a deliberate move
	m.pop.body = claudeNoticeBody()
	m.pop.accent = cYellow // a notice to read, not an error
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
