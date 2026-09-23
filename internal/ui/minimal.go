package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

// The minimal-code saver asks the model for the smallest change that fully
// does the job: reuse before writing, standard library before dependencies,
// nothing speculative. Less code means fewer output tokens and less to
// review. It never trades away validation, data safety, security or
// accessibility. Levels: off (default), lite, full, strict.

var minimalLevels = []string{"off", "lite", "full", "strict"}

var minimalDescs = map[string]string{
	"off":    "the model writes code as it usually would",
	"lite":   "reuse the project and the standard library before writing new code",
	"full":   "go down a reuse-first checklist before any new code; nothing speculative",
	"strict": "every added line is a cost: delete or simplify whenever that does the job",
}

const minimalLite = `Writing code: keep changes small. Reuse what the project already has and the language's standard library before writing new code or adding dependencies. Don't add features, options or abstractions nobody asked for.`

const minimalFull = `Writing code: do the least that fully solves the task.
Before adding code, go down this list and stop at the first answer that works:
1. Is it needed for what was asked? If not, leave it out.
2. Does the project already have it? Reuse or extend that.
3. Does the standard library or the platform provide it? Use that.
4. Does a dependency the project already has do it? Use that; don't add a new dependency for something small.
5. Otherwise, write the smallest clear version.
No speculative options, settings, layers, interfaces, or helpers used once. Change a few lines rather than rewrite.
Never cut these: input validation where untrusted data enters, error handling that prevents data loss, security, accessibility.
Be thorough when reading and investigating; be minimal only in what you write.`

const minimalStrict = minimalFull + `
Treat every added line as a cost. If the task can be done by deleting or simplifying code, do that. When you deliberately leave something out, say so in one line of your reply instead of building it.`

func minimalPrompt(level string) string {
	switch level {
	case "lite":
		return minimalLite
	case "full":
		return minimalFull
	case "strict":
		return minimalStrict
	}
	return ""
}

func (m *Model) minimalLevel() string {
	if m.settings.MinimalCode == "" {
		return "off"
	}
	return m.settings.MinimalCode
}

// openMinimalSlider shows the levels on a slider.
func (m *Model) openMinimalSlider() {
	var cs []choice
	sel := 0
	for i, l := range minimalLevels {
		l := l
		if l == m.minimalLevel() {
			sel = i
		}
		cs = append(cs, choice{label: l, value: l, desc: minimalDescs[l], run: func(m *Model) tea.Cmd { return m.setMinimal(l) }})
	}
	m.openSlider("Minimal code", "How much code the model may write · validation, security and accessibility are never cut", cs, sel)
	m.pop.colors = neutralFirst(len(cs))
}

// setMinimal changes the level; without one it opens the slider.
func (m *Model) setMinimal(level string) tea.Cmd {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		m.openMinimalSlider()
		return nil
	}
	if level == m.minimalLevel() {
		return nil
	}
	valid := false
	for _, l := range minimalLevels {
		valid = valid || l == level
	}
	if !valid {
		m.note("minimal code levels: off, lite, full, strict", false)
		return nil
	}
	m.settings.MinimalCode = level
	saveSettings(m.settings)
	if eng, ok := m.client.(*agent.Engine); ok {
		eng.SetStyle(m.applySavers(claude.Options{}).AppendPrompt)
		m.note("minimal code → "+level, true)
		return nil
	}
	return m.restart("minimal code → " + level)
}

const trimDiffPrompt = `Review my uncommitted changes (git diff, plus new files from git status) for code that isn't pulling its weight: anything not needed for the task, anything that duplicates what the project, the standard library or an installed dependency already provides, and anything that could be much shorter and just as clear.
List each finding as path:line, what to remove or replace, and why, the biggest win first. Don't edit anything. Leave validation of untrusted input, error handling that prevents data loss, security and accessibility code alone. If the changes are already lean, say so.`

const trimAllPrompt = `Review this whole project for code that isn't pulling its weight: dead or unused code, hand-written versions of what the standard library or an installed dependency already does, duplicated logic, and abstractions with a single use.
List the findings as path:line, what to remove or replace, and why, the biggest wins first (at most 15). Don't edit anything. Leave validation of untrusted input, error handling that prevents data loss, security and accessibility code alone.`

// trim asks the model to review for over-engineering: the diff, or with
// "all" the whole project.
func (m *Model) trim(arg string) tea.Cmd {
	if strings.TrimSpace(arg) == "all" {
		return m.send(trimAllPrompt)
	}
	return m.send(trimDiffPrompt)
}
