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

// The levels are sent as a system prompt, so every word here is carried on
// every request of the session: full used to be 15 lines and 520 tokens, more
// than the reply it was meant to shorten. The same rules fit in a third of
// that without losing one, because half of the old text said the same thing
// twice ("do the least", then "no speculative options", then "delete what has
// no answer") and the rest spelled out cases the model already knows.
const minimalFull = `Writing code: do the least that fully solves the task.
- Is it needed for what was asked? Does the project, the standard library or a dependency it already has do it? Stop at the first yes; otherwise write the smallest clear version.
- Take the shape from the request, not from nearby code. When it can be read two ways, do the part both readings agree on and say in one line which you took.
- Add a public name only when the request needs one. A standard-library call that expresses the operation is the implementation.
- Never cut input validation, error handling that prevents data loss, security or accessibility, and never trade correctness for brevity: if it needs two functions, write two.
Be thorough when reading; be minimal only in what you write.`

const minimalStrict = minimalFull + `
Read your diff line by line before you finish and delete what the request did not put there; check the result still compiles and does the whole job. Say in one line what you left out.`

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
