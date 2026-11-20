package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MCP servers that use OAuth don't hand out a token in their config: they
// answer 401 with a WWW-Authenticate header pointing at their protected
// resource metadata, the client registers itself, and the user approves in a
// browser. This is the flow Claude Code runs when you add such a server, and
// the one /mcp's "Sign in" button drives.

// oauthMetadata is what an MCP server and its authorization server publish.
type oauthMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`

	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupportedByServer       []string `json:"-"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
}

// MCPOAuth describes what a server needs from the user, for the UI to offer.
type MCPOAuth struct {
	Server string
	// MetadataURL is where the 401 pointed, when the server said so.
	MetadataURL string
}

// MCPNeedsOAuth reports whether a server's 401 asks for OAuth: the response
// carries a WWW-Authenticate header naming a resource_metadata document.
func MCPNeedsOAuth(headers http.Header) bool {
	h := strings.ToLower(headers.Get("WWW-Authenticate"))
	return strings.Contains(h, "resource_metadata")
}

// resourceMetadataURL pulls the document address out of the challenge.
func resourceMetadataURL(challenge, base string) string {
	for _, part := range strings.Split(challenge, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "resource_metadata="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	// A server that doesn't name it uses the standard location.
	if u, err := url.Parse(base); err == nil {
		return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"
	}
	return ""
}

// MCPLogin runs the OAuth flow for one server and returns the bearer token to
// put in its headers. open is called with the authorization URL, and the
// callback comes back on a loopback listener.
func MCPLogin(ctx context.Context, serverURL, metadataURL string, open func(string)) (string, error) {
	meta, err := discoverOAuth(ctx, serverURL, metadataURL)
	if err != nil {
		return "", err
	}
	// The callback must be registered before the user is sent away, so the
	// client is registered first: these servers accept a public client with
	// PKCE and no secret (token_endpoint_auth_method "none").
	verifier, err := randomVerifier()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", l.Addr().(*net.TCPAddr).Port)

	clientID, err := registerClient(ctx, meta.RegistrationEndpoint, redirect)
	if err != nil {
		return "", err
	}

	codes := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := r.URL.Query().Get("error"); e != "" {
			fmt.Fprintf(w, "<html><body style=\"font-family:sans-serif;padding:3em\"><h2>Authorization failed</h2><p>%s</p></body></html>", e)
			select {
			case codes <- "":
			default:
			}
			return
		}
		code := r.URL.Query().Get("code")
		fmt.Fprint(w, "<html><body style=\"font-family:sans-serif;padding:3em\"><h2>Signed in</h2><p>You can close this tab and return to teveus.</p></body></html>")
		select {
		case codes <- code:
		default:
		}
	})}
	go srv.Serve(l)
	defer srv.Close()

	scopes := meta.ScopesSupported
	if len(scopes) == 0 {
		scopes = []string{"mcp:read", "mcp:write"}
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {strings.Join(scopes, " ")},
		"resource":              {meta.Resource},
		"state":                 {clientID},
	}
	open(meta.AuthorizationEndpoint + "?" + q.Encode())

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var code string
	select {
	case code = <-codes:
	case <-ctx.Done():
		return "", errors.New("sign-in timed out")
	}
	if code == "" {
		return "", errors.New("authorization was refused")
	}
	return exchangeCode(ctx, meta.TokenEndpoint, clientID, redirect, code, verifier)
}

// discoverOAuth reads the protected-resource metadata, then the authorization
// server's own document, and fills in the endpoints to use.
func discoverOAuth(ctx context.Context, serverURL, metadataURL string) (oauthMetadata, error) {
	var res oauthMetadata
	if err := getJSON(ctx, metadataURL, &res); err != nil {
		return res, fmt.Errorf("resource metadata: %w", err)
	}
	issuer := strings.TrimRight(serverURL, "/")
	if len(res.AuthorizationServers) > 0 {
		issuer = res.AuthorizationServers[0]
	}
	var as oauthMetadata
	if err := getJSON(ctx, strings.TrimRight(issuer, "/")+"/.well-known/oauth-authorization-server", &as); err != nil {
		// Fall back to the resource document's own fields.
		if res.AuthorizationEndpoint == "" {
			return res, fmt.Errorf("authorization server metadata: %w", err)
		}
		as = res
	}
	if as.AuthorizationEndpoint == "" || as.TokenEndpoint == "" {
		return as, errors.New("the server didn't publish an authorization or token endpoint")
	}
	as.Resource = res.Resource
	if len(res.ScopesSupported) > 0 {
		as.ScopesSupported = res.ScopesSupported
	}
	return as, nil
}

// registerClient registers teveus as a public client (no secret), which these
// servers allow for local apps using PKCE.
func registerClient(ctx context.Context, endpoint, redirect string) (string, error) {
	if endpoint == "" {
		// Some servers only accept a fixed client_id.
		return "teveus", nil
	}
	body := map[string]any{
		"client_name":                "teveus",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("client registration: %s", resp.Status)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ClientID == "" {
		return "", errors.New("client registration returned no client_id")
	}
	return out.ClientID, nil
}

func exchangeCode(ctx context.Context, endpoint, clientID, redirect, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("token exchange: %s", resp.Status)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token exchange: %s", firstNonEmpty(out.Error, resp.Status))
	}
	return out.AccessToken, nil
}

func getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := httpDo(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func randomVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// OpenBrowserFor is the opener MCPLogin uses; a var so tests can replace it.
var OpenBrowserFor = OpenBrowser
