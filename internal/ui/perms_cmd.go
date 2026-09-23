package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// showPermissions lists and edits the Direct API engine's saved permission
// rules: "/permissions", "/permissions allow Bash(npm test:*)",
// "/permissions deny WebFetch". Claude Code keeps its own rules, so the
// command goes to it on that engine.
func (m *Model) showPermissions(arg string) tea.Cmd {
	eng, ok := m.client.(*agent.Engine)
	if !ok {
		return m.send(strings.TrimSpace("/permissions " + arg))
	}
	if verb, rule, _ := strings.Cut(strings.TrimSpace(arg), " "); verb == "allow" || verb == "deny" {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			m.note("usage: /permissions "+verb+" Bash(npm test:*)", false)
			return nil
		}
		if err := eng.AddPermissionRule(rule, verb == "deny"); err != nil {
			m.add(&block{kind: kindError, text: err.Error()})
		} else {
			m.note(fmt.Sprintf("✓ %s %s in this project", verb, rule), true)
		}
		return nil
	}
	rs := eng.PermissionRules()
	var sb strings.Builder
	sb.WriteString("**Permission rules**\n\n")
	if len(rs.Allow)+len(rs.Deny) == 0 {
		sb.WriteString("None yet. Press **a** on an approval prompt to save one.\n")
	}
	for _, r := range rs.Allow {
		sb.WriteString("- allow `" + r + "`\n")
	}
	for _, r := range rs.Deny {
		sb.WriteString("- deny `" + r + "`\n")
	}
	fmt.Fprintf(&sb, "\nAdd one with `/permissions allow Bash(go test:*)` or `/permissions deny WebFetch`. Rules are kept in `%s`, never in your project.",
		filepath.Join(configDir(), "permissions.json"))
	m.add(&block{kind: kindCmdOut, text: sb.String()})
	m.refresh()
	if len(rs.Allow)+len(rs.Deny) > 0 {
		var cs []choice
		for _, r := range append(append([]string(nil), rs.Allow...), rs.Deny...) {
			r := r
			cs = append(cs, choice{label: r, desc: "remove this rule", run: func(m *Model) tea.Cmd {
				if err := eng.RemovePermissionRule(r); err != nil {
					m.add(&block{kind: kindError, text: err.Error()})
				} else {
					m.note("removed "+r, true)
				}
				return nil
			}})
		}
		m.openPicker("Remove a rule? (esc keeps them all)", cs, 0)
	}
	return nil
}
