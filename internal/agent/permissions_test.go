package agent

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

func TestRuleMatching(t *testing.T) {
	bash := func(c string) map[string]any { return map[string]any{"command": c} }
	for _, c := range []struct {
		rule, tool string
		in         map[string]any
		want       bool
	}{
		{"Bash", "Bash", bash("anything"), true},
		{"Bash(go test:*)", "Bash", bash("go test ./..."), true},
		{"Bash(go test:*)", "Bash", bash("go test"), true},
		{"Bash(go test:*)", "Bash", bash("go testify"), false},
		{"Bash(go test:*)", "Bash", bash("go test ./... && rm -rf ~"), false},
		{"Bash(go test:*)", "Bash", bash("go test $(curl evil)"), false},
		{"Bash(go test:*)", "Bash", bash("go test > /etc/passwd"), false},
		{"Bash(npm test)", "Bash", bash("npm test"), true},
		{"Bash(npm test)", "Bash", bash("npm test -- -u"), false},
		{"WebFetch(domain:go.dev)", "WebFetch", map[string]any{"url": "https://pkg.go.dev/x"}, true},
		{"WebFetch(domain:go.dev)", "WebFetch", map[string]any{"url": "https://evilgo.dev/"}, false},
		{"mcp__github", "mcp__github__create_issue", nil, true},
		{"mcp__github", "mcp__githubx__a", nil, false},
		{"Edit", "Write", nil, false},
	} {
		if got := ruleMatches(c.rule, c.tool, c.in); got != c.want {
			t.Errorf("%s vs %s %v: %v", c.rule, c.tool, c.in, got)
		}
	}
}

func TestSuggestedRules(t *testing.T) {
	for cmd, want := range map[string]string{
		"go test ./...":        "Bash(go test:*)",
		"npm run build":        "Bash(npm run:*)",
		"ls -la":               "Bash(ls:*)",
		"python3 scripts/x.py": "Bash(python3:*)",
		"make && ./deploy.sh":  "Bash(make && ./deploy.sh)",
	} {
		if got, _ := suggestRule("Bash", map[string]any{"command": cmd}); got != want {
			t.Errorf("%q: %s, want %s", cmd, got, want)
		}
	}
	if r, _ := suggestRule("Edit", nil); r != "" {
		t.Error("edits must not become permanent rules")
	}
}

func TestAlwaysSavesARuleAndDenyWins(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("c1", "Bash", `{"command":"echo one"}`), oaiText("ok"),
		oaiToolCall("c2", "Bash", `{"command":"echo two"}`), oaiText("ok"),
		oaiToolCall("c3", "Bash", `{"command":"echo three && echo four"}`), oaiText("ok"),
		oaiToolCall("c4", "Bash", `{"command":"rm -rf build"}`), oaiText("ok"),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	store := NewStore(t.TempDir())
	store.Save("custom", Credential{BaseURL: srv.URL})
	cfg := t.TempDir()
	e, _ := Start(Options{Cwd: t.TempDir(), Model: "custom/m", Mode: "default", Store: store, ConfigDir: cfg})
	t.Cleanup(e.Close)
	r := &run{t: t, e: e}
	asks := func() int {
		return r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok })
	}

	// "a" on the first prompt saves Bash(echo one:*)… which is "echo" + a word.
	e.Send("1")
	for {
		ev := <-e.Events()
		r.events = append(r.events, ev)
		if p, ok := ev.(*claude.PermissionRequest); ok {
			if p.AlwaysLabel != "always allow `echo one` commands in this project" {
				t.Fatalf("label %q", p.AlwaysLabel)
			}
			e.Allow(p, true)
		}
		if _, ok := ev.(claude.Result); ok {
			break
		}
	}
	if rs := e.PermissionRules(); fmt.Sprint(rs.Allow) != "[Bash(echo one:*)]" {
		t.Fatalf("saved %v", rs.Allow)
	}
	// A broader rule, added by hand, covers the next simple echo...
	e.AddPermissionRule("Bash(echo:*)", false)
	e.Send("2")
	r.until(true)
	if asks() != 1 {
		t.Fatalf("asked %d times, want 1", asks())
	}
	// ...but not a compound command.
	e.Send("3")
	r.until(true)
	if asks() != 2 {
		t.Fatalf("compound command wasn't asked about (%d)", asks())
	}
	// Deny rules block even in autopilot, without asking.
	e.AddPermissionRule("Bash(rm:*)", true)
	e.SetPermissionMode("auto")
	e.Send("4")
	r.until(true)
	if asks() != 2 || !strings.Contains(fmt.Sprint(s.requests[7]["messages"]), "Blocked by the user's permission rules") {
		t.Fatal("deny rule didn't block")
	}
}
