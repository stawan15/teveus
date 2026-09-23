package agent

import (
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

func TestHTMLText(t *testing.T) {
	got := htmlText(`<html><head><title>Docs</title><style>x{}</style></head><body>
<nav>menu</nav><h2>Install</h2><p>Run <code>go get</code> then
<a href="https://ex.com/a">read more</a>.</p><ul><li>one</li><li>two</li></ul>
<pre>line 1
line 2</pre><script>alert(1)</script></body></html>`)
	for _, want := range []string{"# Docs", "## Install", "Run `go get` then read more (https://ex.com/a).", "- one\n- two", "```\nline 1\nline 2\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"menu", "alert", "x{}"} {
		if strings.Contains(got, bad) {
			t.Errorf("kept %q:\n%s", bad, got)
		}
	}
}

func TestWebFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "teveus/") {
			t.Errorf("user agent %q", r.UserAgent())
		}
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<p>Hello <b>web</b></p>")
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{0, 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if out, err := runWebFetch(t.Context(), nil, map[string]any{"url": srv.URL + "/page"}); err != nil || out != "Hello web" {
		t.Fatalf("page: %q %v", out, err)
	}
	for _, u := range []string{srv.URL + "/bin", srv.URL + "/missing", "file:///etc/passwd", "ftp://x"} {
		if _, err := runWebFetch(t.Context(), nil, map[string]any{"url": u}); err == nil {
			t.Errorf("%s: no error", u)
		}
	}
}

func TestWebSearchNeedsAKeyAndUsesIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "bk" || r.URL.Query().Get("q") != "go generics" {
			t.Errorf("request %v %v", r.Header, r.URL)
		}
		io.WriteString(w, `{"web":{"results":[{"title":"Tutorial","url":"https://go.dev/doc/tutorial/generics","description":"Learn <strong>generics</strong>"}]}}`)
	}))
	defer srv.Close()
	old := SearchProviders[0].BaseURL
	SearchProviders[0].BaseURL = srv.URL
	defer func() { SearchProviders[0].BaseURL = old }()
	t.Setenv("BRAVE_API_KEY", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")

	store := NewStore(t.TempDir())
	e := &Engine{opts: Options{Store: store}}
	if _, ok := findTool(e.tools(true), "WebSearch"); ok {
		t.Fatal("WebSearch offered without a key")
	}
	store.Save("brave", Credential{Key: "bk"})
	ws, ok := findTool(e.tools(true), "WebSearch")
	if !ok {
		t.Fatal("WebSearch missing with a key")
	}
	out, err := ws.run(t.Context(), nil, map[string]any{"query": "go generics"})
	if err != nil || !strings.Contains(out, "1. Tutorial\n   https://go.dev/doc/tutorial/generics\n   Learn generics") {
		t.Fatalf("%q %v", out, err)
	}
	if _, ok := findTool(e.tools(false), "Task"); ok {
		t.Fatal("subagents must not get Task")
	}
}

func TestTaskRunsANestedSubagent(t *testing.T) {
	s := &script{replies: []string{
		oaiToolCall("task_1", "Task", `{"description":"find config","prompt":"Where is the config loaded?"}`),
		oaiToolCall("sub_1", "Grep", `{"pattern":"LoadConfig"}`),
		oaiText("LoadConfig is in cfg.go:1."),
		oaiText("It's loaded in cfg.go."),
	}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, dir := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	os.WriteFile(filepath.Join(dir, "cfg.go"), []byte("func LoadConfig() {}\n"), 0o644)
	r.e.Send("where is config loaded?")
	res := r.until(true)
	if res.IsError || len(s.requests) != 4 {
		t.Fatalf("result=%+v requests=%d", res, len(s.requests))
	}
	// The subagent starts fresh: its prompt only, and read-only tools.
	sub := s.requests[1]
	if msgs := sub["messages"].([]any); len(msgs) != 2 || !strings.Contains(fmt.Sprint(msgs[1]), "Where is the config loaded?") {
		t.Fatalf("subagent messages: %v", msgs)
	}
	var names []string
	for _, tl := range sub["tools"].([]any) {
		names = append(names, tl.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	if got := strings.Join(names, ","); got != "Read,Grep,Glob,WebFetch" {
		t.Errorf("subagent tools = %s", got)
	}
	// Its tool calls are nested under the Task call, and its report is the
	// Task result the main loop sees.
	nested := r.count(func(e claude.Event) bool { m, ok := e.(claude.AssistantMessage); return ok && m.Parent == "task_1" })
	if nested != 2 {
		t.Fatalf("nested assistant messages = %d", nested)
	}
	if !strings.Contains(fmt.Sprint(s.requests[3]["messages"]), "LoadConfig is in cfg.go:1.") {
		t.Fatal("report didn't reach the main conversation")
	}
	if n := r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok }); n != 0 {
		t.Fatalf("read-only subagent asked %d times", n)
	}
}

func TestOpenRouterMarksCacheForClaude(t *testing.T) {
	for model, want := range map[string]bool{"anthropic/claude-sonnet-5": true, "google/gemini-3.5-flash": true, "openai/gpt-5.4": false} {
		msgs := []map[string]any{{"role": "system", "content": "sys"}, {"role": "user", "content": "hi"}}
		if explicitCache(model) {
			markCache(msgs[0])
			markCache(msgs[1])
		}
		got := strings.Contains(fmt.Sprint(msgs), "ephemeral")
		if got != want {
			t.Errorf("%s: cache marked = %v", model, got)
		}
	}
}
