package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSavesReadsAndDeletes(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, src, _ := s.Lookup(Provider{ID: "openai"}); src != FromNone {
		t.Fatalf("an empty store reports %v", src)
	}

	if err := s.Save("openai", Credential{Key: "sk-1"}); err != nil {
		t.Fatal(err)
	}
	c, src, _ := s.Lookup(Provider{ID: "openai"})
	if src != FromStore || c.Key != "sk-1" {
		t.Fatalf("lookup = %+v %v", c, src)
	}

	// The file has to stay private: it holds a key.
	fi, err := os.Stat(filepath.Join(filepath.Dir(s.path), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json is %o, want 600", perm)
	}

	if err := s.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, src, _ := s.Lookup(Provider{ID: "openai"}); src != FromNone {
		t.Fatal("the credential survived Delete")
	}
}

func TestStoreFallsBackToTheEnvironment(t *testing.T) {
	// An env var is used when nothing is saved, and the name is reported so
	// the UI can say where it came from.
	s := NewStore(t.TempDir())
	t.Setenv("OPENAI_API_KEY", "from-env")
	c, src, name := s.Lookup(Provider{ID: "openai", EnvKeys: []string{"OPENAI_API_KEY"}})
	if src != FromEnv || c.Key != "from-env" || name != "OPENAI_API_KEY" {
		t.Fatalf("lookup = %+v %v %q", c, src, name)
	}

	// A saved credential wins over the environment.
	s.Save("openai", Credential{Key: "saved"})
	if c, src, _ := s.Lookup(Provider{ID: "openai", EnvKeys: []string{"OPENAI_API_KEY"}}); src != FromStore || c.Key != "saved" {
		t.Fatalf("saved should win: %+v %v", c, src)
	}
}

func TestStoreReportsALocalProviderWithNoKey(t *testing.T) {
	// A local server needs no key, so it's usable with nothing saved.
	s := NewStore(t.TempDir())
	c, src, _ := s.Lookup(Provider{ID: "local", NoKey: true})
	if src != FromLocal || c.Key != "" {
		t.Fatalf("lookup = %+v %v", c, src)
	}
}

func TestVerifyCountsTheModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	n, err := Verify(context.Background(),
		Provider{ID: "custom", Protocol: OpenAI, BaseURL: srv.URL, ModelsPath: "/v1/models"},
		Credential{Key: "k"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 2 {
		t.Fatalf("counted %d models", n)
	}

	// A rejected key comes back as an error, not a count.
	_, err = Verify(context.Background(),
		Provider{ID: "custom", Protocol: OpenAI, BaseURL: "http://127.0.0.1:1", ModelsPath: "/v1/models"},
		Credential{Key: "k"})
	if err == nil {
		t.Fatal("verifying an unreachable provider succeeded")
	}
}

func TestVerifySearchProvider(t *testing.T) {
	// Search providers are checked with one query, and report no model count.
	s := &searchServer{t: t}
	srv := httptest.NewServer(s)
	defer srv.Close()
	// The search providers are a fixed list; point Brave's base URL at the
	// test server so the real request shape is exercised.
	p := Provider{ID: "brave", Name: "Brave Search", BaseURL: srv.URL}
	if n, err := Verify(context.Background(), p, Credential{Key: "k"}); err != nil || n != 0 {
		t.Fatalf("verify = %d, %v", n, err)
	}
	if !s.called {
		t.Fatal("the search endpoint was never asked")
	}
}

// searchServer answers a search request once.
type searchServer struct {
	t      *testing.T
	called bool
}

func (s *searchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.called = true
	// Brave authenticates with a header; the query carries the search itself.
	if r.Header.Get("X-Subscription-Token") != "k" {
		http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	if r.URL.Query().Get("q") == "" {
		http.Error(w, "no query", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"web":{"results":[{"title":"t","url":"u","description":"d"}]}}`))
}

func TestApiErrorMessageIsReadable(t *testing.T) {
	// Every shape of error body has to come out as the provider's own words.
	cases := []struct {
		body string
		want string
	}{
		{`{"error":{"message":"bad key"}}`, "bad key"},
		{`{"message":"too long"}`, "too long"},
		{`[{"error":{"message":"google style"}}]`, "google style"},
		{`not json at all`, "not json at all"},
	}
	for _, c := range cases {
		resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(c.body))}
		err := apiError(resp)
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("body %q gave %q, want it to mention %q", c.body, err.Error(), c.want)
		}
	}
	// The common failures say what to do about them.
	for code, word := range map[int]string{401: "/login", 402: "credits", 429: "rate"} {
		resp := &http.Response{StatusCode: code, Status: fmt.Sprintf("%d x", code),
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"x"}}`))}
		if got := strings.ToLower(apiError(resp).Error()); !strings.Contains(got, word) {
			t.Errorf("status %d = %q, want it to mention %q", code, got, word)
		}
	}
}

func TestRejectsParamSpotsTheOffender(t *testing.T) {
	if !rejectsParam(errString("400 invalid request: unknown field reasoning_effort"), "reasoning") {
		t.Fatal("a rejection naming the parameter wasn't recognised")
	}
	if rejectsParam(errString("rate limited, try later"), "reasoning") {
		t.Fatal("an unrelated error was blamed on the parameter")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestMentionsLevel(t *testing.T) {
	// The two kinds of refusal mean different things, and this tells them
	// apart: the field is unknown, or just this value.
	if mentionsLevel(errString("effort: unsupported value high"), "high") != true {
		t.Fatal("a value-specific refusal wasn't recognised")
	}
	if mentionsLevel(errString("this model does not support effort"), "high") {
		t.Fatal("a refusal of the whole field was read as a value problem")
	}
	if mentionsLevel(nil, "high") {
		t.Fatal("nil error")
	}
	if mentionsLevel(errString("something else"), "") {
		t.Fatal("an empty level")
	}
	if mentionsLevel(errString("unknown parameter effort"), "effort") {
		t.Fatal("an unknown-parameter error was read as a value problem")
	}
}
