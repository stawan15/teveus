package agent

import (
	"bytes"
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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type Credential struct {
	Key     string `json:"key,omitempty"`
	BaseURL string `json:"baseURL,omitempty"`
}

// Store holds credentials in <dir>/auth.json, readable only by the user.
type Store struct{ path string }

func NewStore(dir string) *Store { return &Store{path: filepath.Join(dir, "auth.json")} }

func (s *Store) load() map[string]Credential {
	m := map[string]Credential{}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Store) Save(id string, c Credential) error {
	m := s.load()
	m[id] = c
	return s.write(m)
}

func (s *Store) Delete(id string) error {
	m := s.load()
	delete(m, id)
	return s.write(m)
}

func (s *Store) write(m map[string]Credential) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Source says where a credential came from.
type Source string

const (
	FromNone  Source = ""
	FromStore Source = "saved"
	FromEnv   Source = "env"
	FromLocal Source = "local"
)

// Lookup finds a provider's credential: saved login first, then environment.
func (s *Store) Lookup(p Provider) (Credential, Source, string) {
	if c, ok := s.load()[p.ID]; ok && (c.Key != "" || c.BaseURL != "") {
		return c, FromStore, ""
	}
	for _, env := range p.EnvKeys {
		if v := os.Getenv(env); v != "" {
			return Credential{Key: v}, FromEnv, env
		}
	}
	if p.NoKey {
		return Credential{}, FromLocal, ""
	}
	return Credential{}, FromNone, ""
}

// Verify checks a credential by listing models.
// Search providers are checked with a one-result query and report 0.
func Verify(ctx context.Context, p Provider, c Credential) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if p.IsSearch() {
		return 0, VerifySearch(ctx, p, c)
	}
	models, err := NewClient(p, c).Models(ctx)
	if err != nil {
		return 0, err
	}
	return len(models), nil
}

// OpenRouterLogin runs OpenRouter's OAuth PKCE flow: it opens the browser,
// waits for the localhost callback and exchanges the code for an API key.
func OpenRouterLogin(ctx context.Context, open func(string)) (string, error) {
	verifier := make([]byte, 32)
	rand.Read(verifier)
	v := base64.RawURLEncoding.EncodeToString(verifier)
	sum := sha256.Sum256([]byte(v))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	callback := fmt.Sprintf("http://localhost:%d/callback", l.Addr().(*net.TCPAddr).Port)
	codes := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "<html><body style=\"font-family:sans-serif;padding:3em\"><h2>Logged in to OpenRouter</h2><p>You can close this tab and return to teveus.</p></body></html>")
		select {
		case codes <- code:
		default:
		}
	})}
	go srv.Serve(l)
	defer srv.Close()

	q := url.Values{
		"callback_url":          {callback},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"key_label":             {"teveus"},
	}
	open("https://openrouter.ai/auth?" + q.Encode())

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var code string
	select {
	case code = <-codes:
	case <-ctx.Done():
		return "", errors.New("login timed out")
	}

	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": v, "code_challenge_method": "S256"})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/auth/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Key == "" {
		return "", fmt.Errorf("OpenRouter key exchange failed (%s)", resp.Status)
	}
	return out.Key, nil
}

// OpenBrowser opens a URL with the platform's default handler.
func OpenBrowser(u string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", u).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		exec.Command("xdg-open", u).Start()
	}
}
