package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/agent"
)

// Adding a server by hand goes through the name field, a transport picker and
// a command field, then writes the user's mcp.json.
func TestAddMCPServerByHand(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.askMCPAdd(nil, "")
	if m.pop.mode != popInput {
		t.Fatalf("no name prompt: %v", m.pop.mode)
	}
	m.pop.submit(m, "notes")

	if m.pop.mode != popPicker {
		t.Fatalf("no transport picker: %v", m.pop.mode)
	}
	labels := []string{}
	for _, c := range m.pop.list {
		labels = append(labels, c.label)
	}
	if strings.Join(labels, "|") != "Run a command (stdio)|Connect to a URL (http)" {
		t.Fatalf("transports: %v", labels)
	}
	m.pop.list[0].run(m)

	if m.pop.mode != popInput {
		t.Fatalf("no command prompt: %v", m.pop.mode)
	}
	m.pop.submit(m, "npx -y @modelcontextprotocol/server-filesystem ~/notes")

	cfgs, err := agent.UserMCPConfig(configDir())
	if err != nil {
		t.Fatal(err)
	}
	c := cfgs["notes"]
	if c.Command != "npx" || strings.Join(c.Args, " ") != "-y @modelcontextprotocol/server-filesystem ~/notes" {
		t.Fatalf("saved %+v", c)
	}
}

// The whole thing the user asked about: a server needs auth, the picker offers
// a token, and the server then connects.
func TestMCPAuthThroughThePicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"protocolVersion":"2025-06-18","capabilities":{},"tools":[{"name":"ping"}]}}`))
	}))
	defer srv.Close()

	cfg := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", cfg)
	cfgs := map[string]agent.MCPConfig{"api": {Type: "http", URL: srv.URL}}
	if err := agent.SaveUserMCPConfig(cfg, cfgs); err != nil {
		t.Fatal(err)
	}
	store := agent.NewStore(t.TempDir())
	eng, err := agent.Start(agent.Options{Cwd: t.TempDir(), Model: "openai/x", Mode: "default",
		Store: store, ConfigDir: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// Wait for the 401 to show up in the status.
	var st []agent.MCPServerStatus
	for i := 0; i < 200; i++ {
		st = eng.MCPStatus()
		if len(st) > 0 && strings.Contains(st[0].State, "401") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(st) == 0 || !needsAuth(st[0].State) {
		t.Fatalf("never saw the 401: %+v", st)
	}

	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.client, m.engine = eng, "api"
	cs := m.mcpChoices(eng, st)
	var tokenChoice *choice
	for i := range cs {
		if strings.HasPrefix(cs[i].label, "Add a token for api") {
			tokenChoice = &cs[i]
		}
	}
	if tokenChoice == nil {
		t.Fatalf("no token option: %v", cs)
	}
	tokenChoice.run(m)
	if m.pop.mode != popInput || !m.pop.masked {
		t.Fatal("not a masked prompt")
	}
	// The prompt returns a command; in the app Bubble Tea runs it.
	if cmd := m.pop.submit(m, "good"); cmd != nil {
		cmd()
	}

	// And it connects.
	deadline := 200
	for i := 0; i < deadline; i++ {
		for _, s := range eng.MCPStatus() {
			if s.Name == "api" && s.State == "connected" {
				if len(s.Tools) != 1 || s.Tools[0] != "ping" {
					t.Fatalf("tools: %+v", s)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never connected: %+v", eng.MCPStatus())
}
