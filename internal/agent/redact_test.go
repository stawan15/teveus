package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRedactShapes(t *testing.T) {
	cases := []struct {
		in   string
		want string // a substring that must survive
		gone string // a substring that must not
	}{
		{"key is sk-abcdefghijklmnopqrstuvwxyz012345 here", "sk-", "abcdefghijklmnopqrstuvwxyz012345"},
		{"ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuv", "ANTHROPIC_API_KEY=", "api03-abcdefghijklmnopqrstuv"},
		{"token ghp_abcdefghijklmnopqrstuvwxyz0123", "gh", "abcdefghijklmnopqrstuvwxyz0123"},
		{"aws AKIAIOSFODNN7EXAMPLE done", "AKIA", "IOSFODNN7EXAMPLE"},
		{"Authorization: Bearer abcdefghijklmnopqrstuvwxyz", "Bearer", "abcdefghijklmnopqrstuvwxyz"},
		{"slack xoxb-123456789012-abcdefghij", "xox", "123456789012-abcdefghij"},
		{"postgres://user:hunter2password@db.local/x", "://user:", "hunter2password"},
		{`password="correct horse battery"`, "password=", "correct horse battery"},
		{"AIzaSyA1234567890abcdefghijklmnopqrstuv", "AIza", "SyA1234567890abcdefghijklmnopqrstuv"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: lost %q, got %q", c.in, c.want, got)
		}
		if strings.Contains(got, c.gone) {
			t.Errorf("%q: kept secret %q in %q", c.in, c.gone, got)
		}
		if !strings.Contains(got, maskedPrefix) {
			t.Errorf("%q: no marker in %q", c.in, got)
		}
	}
}

func TestRedactPrivateKeyBlock(t *testing.T) {
	in := "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\nAAAA\n-----END OPENSSH PRIVATE KEY-----\nafter"
	got := Redact(in)
	if strings.Contains(got, "b3BlbnNzaC1rZXktdjEAAAAA") {
		t.Fatalf("private key body survived: %s", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("surrounding text was eaten: %s", got)
	}
	// The key type is what tells the user which key leaked.
	if !strings.Contains(got, "BEGIN OPENSSH PRIVATE KEY") {
		t.Fatalf("the key type was lost: %s", got)
	}
}

// JSON and .env are where keys actually show up, so the quotes have to
// survive: RedactJSON has to keep producing valid JSON.
func TestRedactQuotedValues(t *testing.T) {
	for _, in := range []string{
		`{"password":"hunter2hunter2"}`,
		`{"api_key": "abcdefghijkl123"}`,
		`PASSWORD='abcdefgh12345'`,
	} {
		got := Redact(in)
		if !strings.Contains(got, maskedPrefix) {
			t.Errorf("%q wasn't masked: %q", in, got)
		}
		if strings.Count(got, "]") != strings.Count(got, "[") {
			t.Errorf("%q: unbalanced marker: %q", in, got)
		}
		if strings.Count(got, `"`) != strings.Count(in, `"`) {
			t.Errorf("%q: quotes changed: %q", in, got)
		}
	}
	if !json.Valid(RedactJSON([]byte(`{"password":"hunter2hunter2"}`))) {
		t.Fatal("RedactJSON broke a JSON document")
	}
}

// Redacting twice must not change the marker.
func TestRedactIsIdempotent(t *testing.T) {
	once := Redact("key sk-abcdefghijklmnopqrstuvwxyz and password=abcdefghij")
	if twice := Redact(once); twice != once {
		t.Fatalf("second pass changed %q to %q", once, twice)
	}
}

func TestRedactLeavesOrdinaryText(t *testing.T) {
	for _, s := range []string{
		"run go test ./... and then git status",
		"the password field is hashed with bcrypt",
		"see https://example.com/docs for details",
		"sk- is the prefix used by the provider",
	} {
		if got := Redact(s); got != s {
			t.Errorf("%q was changed to %q", s, got)
		}
	}
}

func TestRedactKnownValues(t *testing.T) {
	dir := t.TempDir()
	// A self-hosted provider key with no recognisable shape, and the phone token.
	store := NewStore(dir)
	if err := store.Save("custom", Credential{Key: "zzz-this-is-my-private-key-123", BaseURL: "https://llm.internal"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "phone-token"), []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	LoadKnownSecrets(dir)

	for _, secret := range []string{"zzz-this-is-my-private-key-123", "0123456789abcdef0123456789abcdef"} {
		got := Redact("the value is " + secret + " ok")
		if strings.Contains(got, secret) {
			t.Errorf("%q survived: %s", secret, got)
		}
		if !strings.Contains(got, "…") || !strings.Contains(got, " ok") {
			t.Errorf("%q: marker or tail text missing: %s", secret, got)
		}
	}
}

func TestRedactJSONKeepsShape(t *testing.T) {
	in := []byte(`{"command":"curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz' https://x","env":{"API_KEY":"sk-abcdefghijklmnopqrstuvwx"},"n":3}`)
	out := RedactJSON(in)
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "abcdefghijklmnopqrstuvwxyz") || strings.Contains(string(out), "sk-abcdefghijklmnopqrstuvwx") {
		t.Fatalf("secret survived: %s", out)
	}
	if v["n"].(float64) != 3 {
		t.Fatalf("shape changed: %s", out)
	}
}

func TestSaveSessionRedacts(t *testing.T) {
	dir := t.TempDir()
	s := Session{ID: "s1", Cwd: "/tmp", Model: "m", History: []Message{
		{Role: "user", Text: "my key is sk-abcdefghijklmnopqrstuvwxyz please use it"},
		{Role: "tool", Result: "ANTHROPIC_API_KEY=sk-ant-api03-zzzzzzzzzzzzzzzzzzzz"},
	}}
	if err := SaveSession(dir, s); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "s1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"abcdefghijklmnopqrstuvwxyz", "sk-ant-api03-zzzzzzzzzzzzzzzzzzzz"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%q was written to disk:\n%s", secret, b)
		}
	}
	// The conversation is still usable: the non-secret words are there.
	if !strings.Contains(string(b), "please use it") {
		t.Fatalf("the message was destroyed:\n%s", b)
	}
	// And it still loads.
	if _, err := LoadSession(dir, "s1"); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
}

func TestScrubSessionsCleansOldFiles(t *testing.T) {
	dir := t.TempDir()
	// A file written before redaction existed.
	old := `{"id":"old","cwd":"/tmp","history":[{"role":"user","text":"key sk-abcdefghijklmnopqrstuvwxyz"}]}`
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := ScrubSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("scrubbed %d, want 1", n)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "old.json"))
	if strings.Contains(string(b), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("the old file still holds the key: %s", b)
	}
	// A second run has nothing to do.
	if n, err := ScrubSessions(dir); err != nil || n != 0 {
		t.Fatalf("second run: %d %v", n, err)
	}
}

func TestCheckpointKeepsFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Chmod on Windows only carries the read-only bit, and Stat reports 0666
		// for an ordinary file, so there is no Unix mode to preserve.
		t.Skip("Windows has no Unix permission bits")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp := &checkpoint{files: map[string][]byte{}}
	cp.remember(p)
	if err := os.WriteFile(p, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.restore(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("restored with %o, want 600", perm)
	}
}
