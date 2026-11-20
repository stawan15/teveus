package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/agent"
)

// /mcp is a panel: running it five times must leave one summary, not five.
func TestMCPPanelReplacesItself(t *testing.T) {
	for n := 1; n <= 5; n++ {
		cmdOut := 0
		for _, b := range mcpRuns(t, n) {
			if b.kind == kindCmdOut {
				cmdOut++
			}
		}
		if cmdOut != 1 {
			t.Fatalf("%d runs left %d summaries", n, cmdOut)
		}
	}
}

// The token prompt names the code the server answered with, not the call it
// failed on ("initialize: 401 …" used to be described as "initialize").
func TestMCPTokenPromptNamesTheCode(t *testing.T) {
	m := New(Config{Dark: true})
	cs := m.mcpChoices(nil, []agent.MCPServerStatus{
		{Name: "p", State: "initialize: 401 Unauthorized: the server needs authorization"},
		{Name: "q", State: "tools/list: 403 Forbidden"},
	})
	var descs []string
	for _, c := range cs {
		if strings.HasPrefix(c.label, "Add a token") {
			descs = append(descs, c.desc)
		}
	}
	if len(descs) != 2 {
		t.Fatalf("descs = %v", descs)
	}
	for i, want := range []string{"401", "403"} {
		if !strings.Contains(descs[i], want) {
			t.Errorf("desc %d = %q, want %s", i, descs[i], want)
		}
		if strings.Contains(descs[i], "initialize") || strings.Contains(descs[i], "tools/list") {
			t.Errorf("desc %d names the call instead of the code: %q", i, descs[i])
		}
	}
}

// mcpRuns returns the blocks after writing the panel n times.
func mcpRuns(t *testing.T, n int) []*block {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	st := []agent.MCPServerStatus{{Name: "pastel", Scope: "user", State: "initialize: 401 Unauthorized"}}
	for i := 0; i < n; i++ {
		m.reportMCP(st)
	}
	return m.blocks
}

// A server's lines have to survive the gutter: a long path used to be wrapped
// by the frame's final fit, after the "│" was added, so it split without one.
func TestMCPPanelKeepsItsGutter(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.reportMCP([]agent.MCPServerStatus{{
		Name: "pastel", Scope: "user",
		State:  "initialize: 401 Unauthorized: needs authorization",
		Target: "https://banana.bananabreaking.com/a/very/long/path/that/has/no/spaces/at/all/api/mcp",
	}})
	m.refresh()
	for i, l := range m.lines {
		p := ansi.Strip(l)
		if strings.TrimSpace(p) == "" {
			continue
		}
		if !strings.Contains(p, "│") {
			t.Errorf("row %d lost its gutter: %q", i, p)
		}
	}
}

// Each server is scannable: the name and its state, then what it runs.
func TestMCPPanelPutsTheTargetOnItsOwnLine(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.reportMCP([]agent.MCPServerStatus{{
		Name: "notes", Scope: "project", State: "connected", Tools: []string{"read_file"},
		Target: "npx -y server-filesystem ~/notes",
	}})
	m.refresh()
	var rows []string
	for _, l := range m.lines {
		if p := strings.TrimRight(ansi.Strip(l), " "); strings.TrimSpace(p) != "" {
			rows = append(rows, p)
		}
	}
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "connected npx") {
		t.Errorf("the target runs on from the state:\n%s", joined)
	}
	if !strings.Contains(joined, "~/notes") || !strings.Contains(joined, "read_file") {
		t.Errorf("target or tools missing:\n%s", joined)
	}
}
