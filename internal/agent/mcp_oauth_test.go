package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// oauthMCP is a stateless MCP endpoint that only accepts a token it issued, and
// publishes the OAuth documents a client needs — the shape of a hosted server
// like Pastel.
type oauthMCP struct {
	srv        *httptest.Server
	lastToken  string
	registered string
	challenge  string
	code       string
	verifier   string
	authorize  string // the URL MCPLogin was sent to
}

func newOAuthMCP(t *testing.T) *oauthMCP {
	t.Helper()
	o := &oauthMCP{code: "the-code"}
	mux := http.NewServeMux()

	// The resource metadata, at the location the 401 names.
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"resource":              o.srv.URL + "/api/mcp",
			"authorization_servers": []string{o.srv.URL},
			"scopes_supported":      []string{"mcp:read", "mcp:write"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                o.srv.URL,
			"authorization_endpoint":                o.srv.URL + "/oauth/authorize",
			"token_endpoint":                        o.srv.URL + "/oauth/token",
			"registration_endpoint":                 o.srv.URL + "/oauth/register",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		o.registered = "client-123"
		json.NewEncoder(w).Encode(map[string]any{"client_id": o.registered})
	})
	// The browser step is skipped: the test calls back itself. This records
	// the authorize URL so the callback can be built from it.
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		o.authorize = o.srv.URL + r.URL.String()
		o.challenge = r.URL.Query().Get("code_challenge")
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != o.code {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		// The verifier has to hash to the challenge we were sent.
		o.verifier = r.Form.Get("code_verifier")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "token_type": "Bearer"})
	})
	mux.HandleFunc("/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "error": map[string]any{
				"code": -32601, "message": "This MCP endpoint only accepts POST."}, "id": nil})
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-abc" {
			o.lastToken = got
			w.Header().Set("WWW-Authenticate", `Bearer realm="Pastel MCP", resource_metadata="`+
				o.srv.URL+`/.well-known/oauth-protected-resource"`)
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
			`,"result":{"protocolVersion":"` + mcpProtocol + `","capabilities":{},"tools":[{"name":"draw"}]}}`))
	})
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

func TestMCPOAuthChallengeIsRecognised(t *testing.T) {
	h := http.Header{}
	h.Set("WWW-Authenticate", `Bearer realm="Pastel MCP", resource_metadata="https://x/.well-known/oauth-protected-resource"`)
	if !MCPNeedsOAuth(h) {
		t.Fatal("an OAuth challenge was not recognised")
	}
	if got := resourceMetadataURL(h.Get("WWW-Authenticate"), "https://x/api/mcp"); got != "https://x/.well-known/oauth-protected-resource" {
		t.Errorf("url = %q", got)
	}

	plain := http.Header{}
	plain.Set("WWW-Authenticate", `Bearer realm="x"`)
	if MCPNeedsOAuth(plain) {
		t.Error("a plain bearer challenge was taken for OAuth")
	}
	// A server that names no document uses the standard location.
	if got := resourceMetadataURL(`Bearer realm="x"`, "https://h.example/api/mcp"); got != "https://h.example/.well-known/oauth-protected-resource" {
		t.Errorf("fallback = %q", got)
	}
}

// The whole flow: 401 with an OAuth challenge, browser sign-in, token stored,
// server connected.
func TestMCPOAuthSignIn(t *testing.T) {
	o := newOAuthMCP(t)
	cfgDir := t.TempDir()
	writeMCP(t, filepath.Join(cfgDir, "mcp.json"), map[string]MCPConfig{
		"pastel": {Type: "http", URL: o.srv.URL + "/api/mcp"},
	})
	eng, err := Start(Options{Cwd: t.TempDir(), Model: "openai/x", Mode: "default",
		Store: NewStore(t.TempDir()), ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Wait for the 401 to be reported as needing OAuth.
	var st MCPServerStatus
	deadline := 200
	for i := 0; i < deadline; i++ {
		for _, s := range eng.MCPStatus() {
			st = s
		}
		if st.NeedsOAuth {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !st.NeedsOAuth {
		t.Fatalf("not reported as OAuth: %+v", st)
	}
	if st.OAuthURL == "" || !strings.Contains(st.OAuthURL, "oauth-protected-resource") {
		t.Errorf("challenge url = %q", st.OAuthURL)
	}

	// The "browser": the test plays the user, approving and following the
	// redirect back to the loopback listener.
	open := func(authorizeURL string) {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			t.Errorf("authorize url: %v", err)
			return
		}
		q := u.Query()
		cb, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			t.Errorf("redirect: %v", err)
			return
		}
		q2 := cb.Query()
		q2.Set("code", o.code)
		q2.Set("state", q.Get("state"))
		cb.RawQuery = q2.Encode()
		go http.Get(cb.String())
	}
	if err := eng.MCPLoginOAuth(context.Background(), "pastel", open); err != nil {
		t.Fatal(err)
	}

	// The token was stored and the server reconnected with it.
	cfgs, err := UserMCPConfig(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfgs["pastel"].Headers["Authorization"]; got != "Bearer tok-abc" {
		t.Fatalf("stored %q", got)
	}
	if o.verifier == "" {
		t.Error("no PKCE verifier was sent")
	}
	if o.registered != "client-123" {
		t.Error("the client was not registered")
	}
	var connected MCPServerStatus
	for i := 0; i < deadline; i++ {
		for _, s := range eng.MCPStatus() {
			connected = s
		}
		if connected.State == "connected" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if connected.State != "connected" || len(connected.Tools) != 1 {
		t.Fatalf("after sign-in: %+v", connected)
	}
}

// The verifier has to match the challenge that was sent to the browser, or a
// real server rejects the exchange.
func TestMCPOAuthPKCEMatches(t *testing.T) {
	o := newOAuthMCP(t)
	open := func(authorizeURL string) {
		u, _ := url.Parse(authorizeURL)
		// A browser visits the authorize page first; that is what records the
		// challenge the token exchange is checked against.
		http.Get(authorizeURL)
		cb, _ := url.Parse(u.Query().Get("redirect_uri"))
		q := cb.Query()
		q.Set("code", o.code)
		cb.RawQuery = q.Encode()
		go http.Get(cb.String())
	}
	token, err := MCPLogin(context.Background(), o.srv.URL+"/api/mcp",
		o.srv.URL+"/.well-known/oauth-protected-resource", open)
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-abc" {
		t.Fatalf("token = %q", token)
	}
	// S256: base64url(sha256(verifier)) == the challenge we sent.
	if o.challenge == "" || o.verifier == "" {
		t.Fatal("challenge or verifier missing")
	}
	if got := s256(o.verifier); got != o.challenge {
		t.Errorf("challenge %q != S256(verifier) %q", o.challenge, got)
	}
}

func s256(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
