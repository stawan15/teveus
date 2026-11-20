package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// tokenServer answers 401 until it sees the bearer token, which is what the
// "add a token" button in /mcp is for.
func tokenServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	want := "good"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want {
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
		w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) +
			`,"result":{"protocolVersion":"` + mcpProtocol + `","capabilities":{},"tools":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &want
}

func waitForState(t *testing.T, m *mcpManager, name, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.status() {
			if s.Name == name && s.State == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached %q: %+v", name, want, m.status())
}

// A server that answers 401, then connects once its token is in mcp.json: the
// whole flow the /mcp picker offers.
func TestMCPTokenTurnsA401IntoAConnection(t *testing.T) {
	srv, _ := tokenServer(t)
	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{"api": {Type: "http", URL: srv.URL}})

	m := newMCPManager(t.TempDir(), cfgDir)
	m.start(true)
	defer m.close()

	st := m.status()
	if len(st) != 1 || !strings.Contains(st[0].State, "401") {
		t.Fatalf("status = %+v", st)
	}

	// What the UI does: write the token, then reconnect.
	cfgs, err := UserMCPConfig(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := cfgs["api"]
	cfg.Headers = map[string]string{"Authorization": "Bearer good"}
	cfgs["api"] = cfg
	if err := SaveUserMCPConfig(cfgDir, cfgs); err != nil {
		t.Fatal(err)
	}
	if err := m.reconnect("api"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, m, "api", "connected")
}

// reconnect must read the file, not the copy the server started with: that is
// the whole point of retrying after a config change.
func TestMCPReconnectPicksUpAChangedURL(t *testing.T) {
	first, _ := tokenServer(t)
	second, _ := tokenServer(t)
	cfgDir := t.TempDir()
	path := filepath.Join(cfgDir, "mcp.json")
	writeMCP(t, path, map[string]MCPConfig{"api": {Type: "http", URL: first.URL,
		Headers: map[string]string{"Authorization": "Bearer good"}}})

	m := newMCPManager(t.TempDir(), cfgDir)
	m.start(true)
	defer m.close()
	waitForState(t, m, "api", "connected")

	// Point it at another server that rejects the token, then fix the token.
	writeMCP(t, path, map[string]MCPConfig{"api": {Type: "http", URL: second.URL}})
	if err := m.reconnect("api"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	sawFail := false
	for time.Now().Before(deadline) && !sawFail {
		for _, s := range m.status() {
			if s.Name == "api" && strings.Contains(s.State, "401") {
				sawFail = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawFail {
		t.Fatalf("the old config was reused: %+v", m.status())
	}
}

// The user's own config is written 0600 and read back whole, so adding one
// server never drops another.
func TestUserMCPConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if cs, err := UserMCPConfig(dir); err != nil || len(cs) != 0 {
		t.Fatalf("missing file: %v %v", cs, err)
	}
	if err := SaveUserMCPConfig(dir, map[string]MCPConfig{
		"one": {Command: "uvx", Args: []string{"one"}},
		"two": {Type: "http", URL: "https://example.com/mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := UserMCPConfig(dir)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	if cs["one"].Command != "uvx" || len(cs["one"].Args) != 1 || cs["two"].URL == "" {
		t.Errorf("round trip lost data: %+v", cs)
	}
	// Windows has no owner-only bit to set, so the mode there is whatever the
	// filesystem reports; the check is for the platforms that do honour it.
	if fi, err := os.Stat(filepath.Join(dir, "mcp.json")); err != nil {
		t.Error(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}

	// Adding one keeps the others.
	cs["three"] = MCPConfig{Command: "npx"}
	if err := SaveUserMCPConfig(dir, cs); err != nil {
		t.Fatal(err)
	}
	if again, _ := UserMCPConfig(dir); len(again) != 3 {
		t.Errorf("saving dropped a server: %+v", again)
	}
}
