package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
)

// The /mcp picker offers what each server needs: approval, a token after a
// 401, a retry after a failure, and adding one by hand.
func TestMCPChoicesPerState(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	st := []agent.MCPServerStatus{
		{Name: "shared", Scope: "project", State: "needs approval"},
		{Name: "api", Scope: "user", State: "initialize: 401 Unauthorized: the server needs authorization"},
		{Name: "files", Scope: "user", State: "connected", Tools: []string{"read", "write"}},
		{Name: "broken", Scope: "user", State: "exec: file not found"},
	}
	cs := m.mcpChoices(nil, st)
	labels := make([]string, 0, len(cs))
	for _, c := range cs {
		labels = append(labels, c.label)
	}
	got := strings.Join(labels, " | ")
	for _, want := range []string{
		// Every server is a row you can switch on or off; the switch is the
		// first column, so the label is the mark and then the name.
		"shared", "api", "files", "broken",
		"Approve shared",       // a project server waits for approval
		"Add a token for api",  // 401 gets a token prompt
		"Add a server",         // always possible
		"Show the config path", // where it lives
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in: %s", want, got)
		}
	}
	// A plain failure gets no token prompt: it isn't an auth problem.
	if strings.Contains(got, "Add a token for broken") {
		t.Errorf("a broken server got a token prompt: %s", got)
	}
}

// needsAuth has to recognise the messages the HTTP client and the server
// actually produce, not one exact wording.
func TestNeedsAuthMatchesRealMessages(t *testing.T) {
	for _, s := range []string{
		"initialize: 401 Unauthorized: the server needs authorization; set its token in \"headers\"",
		"tools/list: 403 Forbidden",
		"the server needs authorization",
	} {
		if !needsAuth(s) {
			t.Errorf("not recognised: %q", s)
		}
	}
	for _, s := range []string{
		"connected",
		"exec: file not found",
		"initialize: 500 Internal Server Error",
		"dial tcp: connection refused",
	} {
		if needsAuth(s) {
			t.Errorf("mistaken for an auth problem: %q", s)
		}
	}
}

// A failing server's error goes on one line: some carry a header dump.
func TestMCPStateIsOneLine(t *testing.T) {
	long := "initialize: 500\nServer: x\nX-Trace: y"
	if got := shortMCPState(long); strings.Contains(got, "\n") {
		t.Errorf("multi-line state: %q", got)
	}
	if got := shortMCPState(strings.Repeat("x", 400)); len(got) > 121 {
		t.Errorf("not shortened: %d chars", len(got))
	}
}

// The token prompt is masked and writes to the user's own mcp.json.
func TestMCPTokenIsMaskedAndSaved(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	s := agent.MCPServerStatus{Name: "api", Scope: "project", State: "initialize: 401 Unauthorized"}
	m.askMCPToken(nil, s)
	if m.pop.mode != popInput || !m.pop.masked {
		t.Fatalf("not a masked prompt: mode=%v masked=%v", m.pop.mode, m.pop.masked)
	}
	m.pop.submit(m, "secret-token")
	cfgs, err := agent.UserMCPConfig(configDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfgs["api"].Headers["Authorization"]; got != "Bearer secret-token" {
		t.Errorf("saved %q", got)
	}
	// The project's own file is never written to.
	if _, err := filepath.Glob(filepath.Join(configDir(), "..", ".mcp.json")); err != nil {
		t.Error(err)
	}
}
