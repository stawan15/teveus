package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// A server can be switched off and back on. Off means its tools stop being
// offered; its settings and token stay, so switching it on is free.
func TestMCPToggleKeepsItsConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="x"`)
			http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
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
		w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) +
			`,"result":{"protocolVersion":"` + mcpProtocol + `","capabilities":{},"tools":[{"name":"draw"},{"name":"paint"}]}}`))
	}))
	defer srv.Close()

	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{
		"art": {Type: "http", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer good"}},
	})
	eng, err := Start(Options{Cwd: t.TempDir(), Model: "openai/x", Mode: "default",
		Store: NewStore(t.TempDir()), ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	waitConnected(t, eng, "art")
	if n := len(eng.mcpTools()); n != 2 {
		t.Fatalf("tools = %d", n)
	}

	// Off: the tools go, the config stays.
	if err := eng.SetMCPEnabled("art", false); err != nil {
		t.Fatal(err)
	}
	if n := len(eng.mcpTools()); n != 0 {
		t.Fatalf("still offering %d tools", n)
	}
	var off MCPServerStatus
	for _, s := range eng.MCPStatus() {
		if s.Name == "art" {
			off = s
		}
	}
	if !off.Disabled || off.State != "off" {
		t.Fatalf("status = %+v", off)
	}
	cfgs, _ := UserMCPConfig(cfgDir)
	if !cfgs["art"].Disabled {
		t.Error("the choice wasn't saved")
	}
	if cfgs["art"].Headers["Authorization"] != "Bearer good" {
		t.Error("the token was lost")
	}

	// On again: the same config, no re-entry.
	if err := eng.SetMCPEnabled("art", true); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, eng, "art")
	if n := len(eng.mcpTools()); n != 2 {
		t.Fatalf("tools after switching on = %d", n)
	}
}

// A server the user switched off doesn't connect at startup, and is still
// listed.
func TestMCPDisabledAtStartup(t *testing.T) {
	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{
		"quiet": {Command: "npx", Args: []string{"never"}, Disabled: true},
	})
	m := newMCPManager(t.TempDir(), cfgDir)
	m.start(true)
	defer m.close()

	if n := len(m.tools()); n != 0 {
		t.Fatalf("%d tools from a disabled server", n)
	}
	st := m.status()
	if len(st) != 1 || !st[0].Disabled || st[0].State != "off" {
		t.Fatalf("status = %+v", st)
	}
	if st[0].Scope != "user" {
		t.Errorf("scope = %q, want user: the toggle writes it back there", st[0].Scope)
	}
}

// A project server's switch is written to .mcp.json, not to the user's file.
func TestMCPToggleProjectScope(t *testing.T) {
	cfgDir, cwd := t.TempDir(), t.TempDir()
	writeMCP(t, filepath.Join(cwd, ".mcp.json"), map[string]MCPConfig{"p": {Command: "npx", Args: []string{"x"}}})
	// Approve it so the manager knows where it came from.
	m := newMCPManager(cwd, cfgDir)
	if err := m.approve(); err != nil {
		t.Fatal(err)
	}
	defer m.close()

	if err := m.setEnabled("p", false); err != nil {
		t.Fatal(err)
	}
	proj, _, err := readMCPFile(filepath.Join(cwd, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !proj["p"].Disabled {
		t.Error("the project file wasn't updated")
	}
	if cfgs, _ := UserMCPConfig(cfgDir); len(cfgs) != 0 {
		t.Errorf("the user's file gained %+v", cfgs)
	}
}

func waitConnected(t *testing.T, eng *Engine, name string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		for _, s := range eng.MCPStatus() {
			if s.Name == name && s.State == "connected" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never connected: %+v", name, eng.MCPStatus())
}
