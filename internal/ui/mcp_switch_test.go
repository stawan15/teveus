package ui

import (
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
)

// Every server is a row with an on/off switch, and clicking it flips that
// server rather than opening a submenu.
func TestMCPListHasSwitches(t *testing.T) {
	m := New(Config{Dark: true})
	cs := m.mcpChoices(nil, []agent.MCPServerStatus{
		{Name: "pastel", Scope: "user", State: "connected", Tools: []string{"a", "b"},
			Target: "https://x/api/mcp"},
		{Name: "playwright", Scope: "user", State: "connected", Tools: make([]string, 25),
			Target: "npx -y playwright"},
	})
	var rows []string
	for _, c := range cs {
		if strings.Contains(c.label, "pastel") || strings.Contains(c.label, "playwright") {
			rows = append(rows, c.label+" — "+c.desc)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if !strings.Contains(rows[0], "pastel") || !strings.Contains(rows[0], "2 tools") {
		t.Errorf("row 0 = %q", rows[0])
	}
	if !strings.Contains(rows[1], "25 tools") {
		t.Errorf("row 1 = %q", rows[1])
	}
}

// A disabled server says so, and keeps its count for when it comes back.
func TestMCPDisabledRow(t *testing.T) {
	m := New(Config{Dark: true})
	cs := m.mcpChoices(nil, []agent.MCPServerStatus{
		{Name: "pastel", State: "off", Disabled: true, Tools: []string{"a", "b"}},
	})
	if !strings.Contains(cs[0].desc, "off") || !strings.Contains(cs[0].desc, "2 tools when on") {
		t.Errorf("desc = %q", cs[0].desc)
	}
	// And it offers no sign-in or token prompt while off.
	for _, c := range cs[1:] {
		if strings.Contains(c.label, "Sign in") || strings.Contains(c.label, "Add a token") {
			t.Errorf("offered %q for a disabled server", c.label)
		}
	}
}
