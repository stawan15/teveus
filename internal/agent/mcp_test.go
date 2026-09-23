package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

// TestMain doubles as a tiny stdio MCP server when TEVEUS_FAKE_MCP is set,
// so tests exercise a real child process.
func TestMain(m *testing.M) {
	if os.Getenv("TEVEUS_FAKE_MCP") == "1" {
		fakeMCP(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeMCP(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		json.Unmarshal(sc.Bytes(), &req)
		if len(req.ID) == 0 {
			continue // notification
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"$schema": "x", "type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
				map[string]any{"name": "peek", "description": "Look", "annotations": map[string]any{"readOnlyHint": true}},
			}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo: " + fmt.Sprint(req.Params.Arguments["text"])}}}
		default:
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "nope"}})
			continue
		}
		enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

func writeMCP(t *testing.T, path string, servers map[string]MCPConfig) {
	t.Helper()
	b, _ := json.Marshal(mcpFile{MCPServers: servers})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeServer() MCPConfig {
	return MCPConfig{Command: os.Args[0], Args: []string{"-test.run=^$"}, Env: map[string]string{"TEVEUS_FAKE_MCP": "1"}}
}

func TestMCPStdioToolsAndCalls(t *testing.T) {
	cfgDir, cwd := t.TempDir(), t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{"fake": fakeServer()})
	m := newMCPManager(cwd, cfgDir)
	m.start(true)
	defer m.close()
	list := m.tools()
	if len(list) != 2 || list[0].def.Name != "mcp__fake__echo" || list[1].def.Name != "mcp__fake__peek" {
		t.Fatalf("tools = %v (status %+v)", defsOf(list), m.status())
	}
	if _, ok := list[0].def.Schema["$schema"]; ok {
		t.Error("$schema was passed to the model")
	}
	if list[0].access != runsCommands || list[1].access != readOnly {
		t.Errorf("access = %v, %v", list[0].access, list[1].access)
	}
	out, err := list[0].run(t.Context(), nil, map[string]any{"text": "hi"})
	if err != nil || out != "echo: hi" {
		t.Fatalf("call: %q %v", out, err)
	}
}

func TestProjectMCPNeedsApproval(t *testing.T) {
	cfgDir, cwd := t.TempDir(), t.TempDir()
	project := filepath.Join(cwd, ".mcp.json")
	writeMCP(t, project, map[string]MCPConfig{"fake": fakeServer()})

	if got := PendingProjectMCP(cwd, cfgDir); len(got) != 1 || got[0] != "fake" {
		t.Fatalf("pending = %v", got)
	}
	m := newMCPManager(cwd, cfgDir)
	m.start(true)
	if len(m.tools()) != 0 || m.status()[0].State != "needs approval" {
		t.Fatalf("unapproved server started: %+v", m.status())
	}
	if err := m.approve(); err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if len(m.tools()) != 2 || PendingProjectMCP(cwd, cfgDir) != nil {
		t.Fatalf("after approval: %+v", m.status())
	}
	// Any change to the file needs a new approval.
	cfg := fakeServer()
	cfg.Args = append(cfg.Args, "-test.v")
	writeMCP(t, project, map[string]MCPConfig{"fake": cfg})
	if got := PendingProjectMCP(cwd, cfgDir); len(got) != 1 {
		t.Fatal("edited .mcp.json kept its approval")
	}
}

func TestMCPOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result string
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			result = `{"protocolVersion":"` + mcpProtocol + `","capabilities":{}}`
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "s1" {
				t.Error("session id not sent")
			}
			result = `{"tools":[{"name":"time","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			// Answer as an event stream, after an unrelated notification.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"noon\"}]}}\n\n", req.ID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)
	}))
	defer srv.Close()
	t.Setenv("FAKE_TOKEN", "tok")
	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{"clock": {Type: "http", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer ${FAKE_TOKEN}"}}})
	m := newMCPManager(t.TempDir(), cfgDir)
	m.start(true)
	defer m.close()
	list := m.tools()
	if len(list) != 1 {
		t.Fatalf("status %+v", m.status())
	}
	if out, err := list[0].run(t.Context(), nil, nil); err != nil || out != "noon" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestMCPToolReachesTheModel(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("c1", "mcp__fake__echo", `{"text":"yo"}`),
		oaiText("done"),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{"fake": fakeServer()})
	store := NewStore(t.TempDir())
	store.Save("custom", Credential{BaseURL: srv.URL})
	e, _ := Start(Options{Cwd: t.TempDir(), Model: "custom/m", Mode: "default", Store: store, ConfigDir: cfgDir})
	t.Cleanup(e.Close)
	r := &run{t: t, e: e}
	e.Send("echo yo")
	r.until(true)
	if !strings.Contains(fmt.Sprint(s.requests[0]["tools"]), "mcp__fake__echo") {
		t.Fatal("MCP tool not offered")
	}
	if !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "echo: yo") {
		t.Fatal("MCP result not sent back")
	}
	if n := r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok }); n != 1 {
		t.Fatalf("asked %d times, want 1", n)
	}
}
