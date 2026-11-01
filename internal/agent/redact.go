package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Secrets end up in transcripts for honest reasons: someone pastes a key into
// a prompt, a tool prints an .env, `curl -v` echoes an Authorization header.
// Everything that leaves the process or is written to disk goes through here:
// the saved conversation (session.go), the prompt history (ui/extras.go), the
// phone page and the terminal's tool output.
//
// Two layers:
//
//   - patterns catch the well-known shapes (sk-, ghp_, AKIA, PEM blocks…),
//   - known values catch everything else, by matching the actual keys in
//     auth.json and the phone token exactly.
//
// A value that is masked still shows its last four characters, so a person can
// tell which key it was; the terminal and the page keep that hint, the secret
// itself is gone.

// marker is what replaces a secret. The tail lets the user recognise which
// key leaked without putting it back on screen.
const maskedPrefix = "[redacted:"

func maskTail(v string) string {
	if len(v) < 8 {
		return maskedPrefix + "…]"
	}
	return maskedPrefix + "…" + v[len(v)-4:] + "]"
}

// secretPatterns are the shapes worth catching without knowing the value.
// Order matters: the broad ones run last, so a private key block is replaced
// whole before the generic `key = value` rule can nibble at it.
var secretPatterns = []struct {
	re   *regexp.Regexp
	keep string // a prefix that stays readable (sk-, ghp_…): it isn't the secret
}{
	// PEM blocks: the whole body is the secret. The marker names the key type,
	// which is the only part worth showing.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "pem"},
	// Provider key prefixes.
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{20,}`), "sk-ant-"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`), "sk-"},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`), "gh"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), "gh"},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`), "glpat-"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}`), "xox"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "AKIA"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), "AIza"},
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), "npm_"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), ""},
	// "Authorization: Bearer abc…" and friends.
	{regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9\-._~+/]{16,}=*`), "scheme"},
	// key = value, KEY: value, password="…" — a quoted name ("password":"…") too.
	{regexp.MustCompile(`(?i)(["']?)\b(api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|private[_-]?key|password|passwd|passphrase|auth[_-]?token|access[_-]?token|refresh[_-]?token)\b(["']?)(\s*[:=]\s*)(["']?)[^\s"',;]{8,}`), "kv"},
	// A quoted value may hold spaces: password="correct horse battery".
	{regexp.MustCompile(`(?i)(["']?)\b(api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|private[_-]?key|password|passwd|passphrase|auth[_-]?token|access[_-]?token|refresh[_-]?token)\b(["']?)(\s*[:=]\s*)("[^"\n]{8,}"|'[^'\n]{8,}')`), "kv"},
	// user:password@host in a URL.
	{regexp.MustCompile(`://[^/\s:@]{1,64}:[^/\s@]{1,128}@`), "url"},
}

// Redact replaces the secrets it can recognise with a marker. It is safe on
// any text: the input is returned unchanged when nothing matches.
func Redact(s string) string { return redactWith(s, nil) }

var (
	knownMu     sync.RWMutex
	knownValues []string
)

// RememberSecret teaches Redact an exact value, so it is masked even when it
// has no recognisable shape. Values shorter than 8 bytes are ignored: they
// would match far too much ordinary text.
func RememberSecret(values ...string) {
	knownMu.Lock()
	defer knownMu.Unlock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < 8 || strings.HasPrefix(v, maskedPrefix) {
			continue
		}
		seen := false
		for _, k := range knownValues {
			if k == v {
				seen = true
				break
			}
		}
		if !seen {
			knownValues = append(knownValues, v)
		}
	}
	// Longest first: a key that contains another key must be replaced whole.
	sort.Slice(knownValues, func(i, j int) bool { return len(knownValues[i]) > len(knownValues[j]) })
}

func redactWith(s string, extra []string) string {
	if s == "" {
		return s
	}
	knownMu.RLock()
	known := append(append([]string(nil), knownValues...), extra...)
	knownMu.RUnlock()
	sort.Slice(known, func(i, j int) bool { return len(known[i]) > len(known[j]) })
	for _, v := range known {
		if len(v) >= 8 && strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, maskTail(v))
		}
	}
	for _, p := range secretPatterns {
		re, keep := p.re, p.keep
		s = re.ReplaceAllStringFunc(s, func(m string) string { return maskMatch(keep, m) })
	}
	return s
}

// maskMatch keeps the readable part of a match (the header name, the key
// name, the scheme) and drops the secret in the middle.
func maskMatch(keep, m string) string {
	// Already masked (a later rule seeing what an earlier one wrote): leave it,
	// or the marker would be masked again and grow another bracket.
	if strings.Contains(m, maskedPrefix) {
		return m
	}
	switch keep {
	case "":
		return maskTail(m)
	case "scheme":
		// "Bearer abc…" / "Basic abc…": the word stays, the credential goes.
		if i := strings.IndexAny(m, " \t"); i >= 0 {
			head := m[:i]
			pad := m[i:][:len(m[i:])-len(strings.TrimLeft(m[i:], " \t"))]
			return head + pad + maskTail(strings.TrimLeft(m[i:], " \t"))
		}
		return maskTail(m)
	case "kv":
		// `API_KEY=abc123`, `"password":"abc"`, `password = "abc def"`: the
		// name, quotes and separator stay, the value goes. The closing quote is
		// not always part of the match (the value class stops before `"`), so
		// it is only re-added when it was there.
		i := strings.IndexAny(m, ":=")
		if i < 0 {
			return maskTail(m)
		}
		rest := m[i+1:]
		pad := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
		rest = strings.TrimLeft(rest, " \t")
		var open, close string
		if len(rest) > 1 && (rest[0] == '"' || rest[0] == '\'') {
			open = rest[:1]
			rest = rest[1:]
			if strings.HasSuffix(rest, open) {
				close = open
				rest = rest[:len(rest)-1]
			}
		}
		if rest == "" {
			return m
		}
		return m[:i+1] + pad + open + maskTail(rest) + close
	case "pem":
		// "-----BEGIN RSA PRIVATE KEY-----…-----END …": the name of the key
		// type is not the secret, and the body is the whole point.
		nl := strings.Index(m, "\n")
		if nl < 0 {
			return maskTail(m)
		}
		return m[:nl] + "\n[redacted: private key removed]"
	case "url":
		// "://user:password@": the user stays, the password goes.
		start := strings.Index(m, "://")
		if start < 0 {
			return maskTail(m)
		}
		at := strings.LastIndex(m, "@")
		colon := strings.Index(m[start+3:], ":")
		if at < 0 || colon < 0 {
			return maskTail(m)
		}
		colon += start + 3
		if colon > at {
			return maskTail(m)
		}
		return m[:colon+1] + maskTail(m[colon+1:at]) + m[at:]
	default:
		// A provider prefix ("sk-", "ghp_"): it names the key, not the secret.
		if strings.HasPrefix(m, keep) {
			return m[:len(keep)] + maskTail(m[len(keep):])
		}
		return maskTail(m)
	}
}

// RedactJSON masks every string inside a JSON document, leaving its shape
// intact. Tool call inputs are re-sent to the model, so they have to stay
// valid JSON.
func RedactJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []byte(Redact(string(raw)))
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return []byte(Redact(string(raw)))
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case string:
		return Redact(t)
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = redactValue(t[k])
		}
		return t
	default:
		return v
	}
}

// RedactMessage returns a copy of m that is safe to write to disk or to show.
// Reasoning is opaque provider state that must be echoed back byte for byte,
// so it is left alone.
func RedactMessage(m Message) Message {
	m.Text = Redact(m.Text)
	m.Result = Redact(m.Result)
	if len(m.ToolCalls) > 0 {
		calls := make([]ToolCall, len(m.ToolCalls))
		for i, c := range m.ToolCalls {
			calls[i] = c
			calls[i].Input = RedactJSON(c.Input)
		}
		m.ToolCalls = calls
	}
	return m
}

// RedactSession returns a copy of s with every message masked.
func RedactSession(s Session) Session {
	if len(s.History) == 0 {
		return s
	}
	hist := make([]Message, len(s.History))
	for i, m := range s.History {
		hist[i] = RedactMessage(m)
	}
	s.History = hist
	return s
}

// ScrubSessions rewrites the saved conversations in dir with their secrets
// masked, and reports how many files changed. It is what /scrub runs for
// conversations saved before redaction existed.
func ScrubSessions(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		s, err := LoadSession(dir, id)
		if err != nil {
			continue
		}
		clean := RedactSession(s)
		b, err := json.Marshal(clean)
		if err != nil {
			continue
		}
		old, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && string(old) == string(b) {
			continue
		}
		tmp := filepath.Join(dir, e.Name()) + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return n, err
		}
		if err := os.Rename(tmp, filepath.Join(dir, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// LoadKnownSecrets teaches Redact the credentials this install holds: the API
// keys in auth.json and the phone link token. Called once at startup.
func LoadKnownSecrets(configDir string) {
	if configDir == "" {
		return
	}
	var vals []string
	for _, c := range NewStore(configDir).load() {
		if c.Key != "" {
			vals = append(vals, c.Key)
		}
		if u := userinfo(c.BaseURL); u != "" {
			vals = append(vals, u)
		}
	}
	// A key can also come from the environment (Store.Lookup falls back to it).
	for _, p := range Providers {
		for _, env := range p.EnvKeys {
			if v := os.Getenv(env); len(v) >= 8 {
				vals = append(vals, v)
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(configDir, "phone-token")); err == nil {
		vals = append(vals, strings.TrimSpace(string(b)))
	}
	RememberSecret(vals...)
}

// userinfo pulls the password out of a URL's user:password@host part.
func userinfo(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	at := strings.Index(raw[i+3:], "@")
	if at < 0 {
		return ""
	}
	auth := raw[i+3 : i+3+at]
	if c := strings.Index(auth, ":"); c >= 0 {
		return auth[c+1:]
	}
	return ""
}
