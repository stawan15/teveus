package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// mcpRedirectServer answers an MCP POST and refuses anything else the way a
// stateless Streamable-HTTP endpoint does — including the GET that Go's client
// makes out of a redirected POST.
func mcpRedirectServer(t *testing.T, redirect int, got *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		*got = append(*got, r.Method+" "+r.URL.Path)
		http.Redirect(w, r, "/api/mcp/", redirect)
	})
	mux.HandleFunc("/api/mcp/", func(w http.ResponseWriter, r *http.Request) {
		*got = append(*got, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"This MCP endpoint only accepts POST."},"id":null}`))
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
			`,"result":{"protocolVersion":"` + mcpProtocol + `","capabilities":{},"tools":[{"name":"ping"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A server behind a redirect works: Go's client turns a redirected POST into a
// GET and drops the body, which any stateless MCP endpoint refuses with
// "only accepts POST".
func TestMCPWorksBehindARedirect(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		var got []string
		srv := mcpRedirectServer(t, code, &got)

		cfg := t.TempDir()
		writeMCP(t, filepath.Join(cfg, "mcp.json"), map[string]MCPConfig{
			"pastel": {Type: "http", URL: srv.URL + "/api/mcp"},
		})
		m := newMCPManager(t.TempDir(), cfg)
		m.start(true)
		st := m.status()
		m.close()

		if len(st) != 1 || st[0].State != "connected" {
			t.Errorf("%d: state = %+v (requests: %v)", code, st, got)
			continue
		}
		for _, r := range got {
			if strings.HasPrefix(r, "GET ") {
				t.Errorf("%d: sent %s", code, r)
			}
		}
	}
}

// A caller that asks for a GET still gets one.
func TestRedirectKeepsGet(t *testing.T) {
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method)
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method)
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/a", nil)
	resp, err := httpDo(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Join(got, ",") != "GET,GET" {
		t.Errorf("methods = %v", got)
	}
}

// A POST that has no replayable body keeps Go's behaviour rather than
// resending something it cannot reproduce.
func TestRedirectWithoutReplayableBody(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://example.invalid/a", strings.NewReader("body"))
	req.GetBody = nil // a stream the caller can't replay
	prev, _ := http.NewRequest("POST", "http://example.invalid/a", nil)
	next, _ := http.NewRequest("GET", "http://example.invalid/a/", nil)
	if err := keepMethodOnRedirect(next, []*http.Request{prev}); err != nil {
		t.Fatal(err)
	}
	if next.Method != "GET" {
		t.Errorf("method = %q, want the GET Go asked for", next.Method)
	}
	_ = req
}

// The loop guard still stops a redirect cycle.
func TestRedirectLoopGuard(t *testing.T) {
	via := make([]*http.Request, 10)
	for i := range via {
		via[i], _ = http.NewRequest("GET", "http://example.invalid/", nil)
	}
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	if err := keepMethodOnRedirect(req, via); err == nil {
		t.Fatal("a redirect cycle was followed")
	}
}
