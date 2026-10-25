package ui

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// commitPrompt asks the model to write and make the commit itself, so the
// message and the file list come from what it actually changed in this
// conversation, and the engines' attribution rules apply as usual.
const commitPrompt = `Commit the work we just did.

1. Look at what changed: ` + "`git status --short`" + ` and ` + "`git diff HEAD`" + ` (staged, unstaged and new files).
2. Stage the files that belong to this work with ` + "`git add <paths>`" + `. Leave alone anything unrelated to what we did, and never add secrets, keys or .env files; if one of those changed, say so and don't commit it.
3. Write the message the way the project's history does: ` + "`git log --oneline -10`" + ` for the style. One short subject line in the imperative ("Add the parser", not "Added" or "Adds"), then a body that says why the change was needed, wrapped at 72 columns. No bullet lists of files.
4. Make one commit with ` + "`git commit`" + `, then show me ` + "`git log --oneline -1`" + ` and ` + "`git status --short`" + `.

If nothing is staged after step 2, say there is nothing to commit and stop. If a hook fails, read its output and fix the cause rather than using --no-verify. Don't push.`

// commit runs /commit: a check that there is something to commit, then the
// model does the rest with the tools it already has. The check happens first
// so a clean tree doesn't cost a model request.
func (m *Model) commit(arg string) tea.Cmd {
	if m.busy {
		m.note("finish or interrupt the current turn first", false)
		return nil
	}
	m.commitArg = arg
	cwd := m.cwd
	return func() tea.Msg {
		if !insideRepo(cwd) {
			return diffMsg("This folder isn't a git repository.")
		}
		if strings.TrimSpace(gitOut(cwd, "status", "--short")) == "" {
			return diffMsg("Nothing to commit: the working tree is clean.")
		}
		return commitStartMsg{}
	}
}

// commitStartMsg says the tree has changes, so the prompt is worth sending; it
// travels through Update, where sending a message is allowed.
type commitStartMsg struct{}

func (m *Model) startCommit() tea.Cmd {
	text := commitPrompt
	shown := "/commit"
	if a := strings.TrimSpace(m.commitArg); a != "" {
		// "/commit only the parser" steers the message and the file choice.
		text += "\n\nWhat the user wants in this commit: " + a
		shown += " " + a
	}
	// The transcript shows what was typed, not the instructions it sent.
	m.sendShown = shown
	return m.send(text)
}

func insideRepo(dir string) bool {
	return strings.TrimSpace(gitOut(dir, "rev-parse", "--is-inside-work-tree")) == "true"
}

func gitOut(dir string, args ...string) string {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}
