package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stawan15/teveus/internal/claude"
)

// Before Google's Antigravity CLI first runs, the user confirms that teveus is
// starting their own installed program, which they sign in to with Google and
// use under Google's terms. Same reason as Claude Code: teveus runs the
// program, it doesn't replace it, and it must be the user's own choice.
//
// Naming: the engine is labelled "Google (Antigravity CLI)" so nobody reads it
// as a Google product. teveus is independent and says so here.

const agyNotice = "teveus will run the Antigravity CLI (agy), which you installed from Google. You sign in to it with Google, teveus never sees your login, " +
	"and your use is covered by Google's terms. teveus is independent: it isn't made or endorsed by Google."

// agyNoticeRead is how long "Continue" waits, so the notice is read rather
// than pressed through.
const agyNoticeRead = 4 * time.Second

// agyNoticeBody is the notice as four short points, key words in bold.
func agyNoticeBody() []string {
	d := sText.Render
	point := func(c lipgloss.Color, parts ...string) string {
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
		point(cBlue, "teveus runs ", "your own Antigravity CLI", ", installed by you from Google"),
		point(cGreen, "You sign in with Google: teveus ", "never sees your login"),
		point(cYellow, "Your use is covered by ", "Google's terms", " · antigravity.google"),
		point(cAccent2, "teveus is ", "independent", ": not made or endorsed by Google"),
	}
}

func (m *Model) agyConfirmed() bool { return m.settings.AgyNotice || m.cfg.Headless }

// askAgyNotice asks once; then runs after the user agrees.
func (m *Model) askAgyNotice(then func(m *Model) tea.Cmd) {
	m.openPicker("Use the Antigravity CLI?", []choice{
		{label: "Continue with Antigravity CLI", value: "accept", color: cGreen,
			desc: "I've read this · don't ask again", run: func(m *Model) tea.Cmd {
				m.settings.AgyNotice = true
				saveSettings(m.settings)
				return then(m)
			}},
		{label: "Use my own API keys instead", color: cBlue,
			desc: "Gemini API key, OpenRouter, OpenAI, Anthropic or a local model",
			run: func(m *Model) tea.Cmd {
				cmd := m.setEngine("api")
				return tea.Batch(cmd, m.openLogin(""))
			}},
		{label: "Not now", color: cDim, desc: "the Antigravity CLI stays off · /engine to choose later",
			run: func(m *Model) tea.Cmd {
				m.note("Antigravity CLI not started · /engine to choose", false)
				return nil
			}},
	}, 2) // start on "Not now": agreeing takes a deliberate move
	m.pop.body = agyNoticeBody()
	m.pop.accent = cYellow
	m.pop.hold, m.pop.holdEnd = "accept", time.Now().Add(agyNoticeRead)
	m.pop.flat = true
	m.pop.localOnly = true
	m.pop.cancel = func(m *Model) { m.note("Antigravity CLI not started · /engine to choose", false) }
}

// gateAgy holds back starting the Antigravity CLI until the user has confirmed.
// It reports whether the start should wait.
func (m *Model) gateAgy(opts claude.Options) bool {
	if m.engine != "antigravity" || m.agyConfirmed() {
		return false
	}
	if m.cfg.Onboard && !m.settings.Onboarded {
		return true
	}
	if !m.pop.open() {
		m.askAgyNotice(func(m *Model) tea.Cmd { return m.start(opts) })
	}
	return true
}
