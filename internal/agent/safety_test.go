package agent

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

// safetyRun runs one tool call in a mode and reports whether the user was
// asked and what the model was told.
func safetyRun(t *testing.T, mode, cfgDir, call string, setup func(dir string) string) (asked bool, result string) {
	t.Helper()
	s := &script{}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	dir := t.TempDir()
	name, args, _ := strings.Cut(call, " ")
	if setup != nil {
		args = setup(dir)
	}
	s.replies = []string{oaiToolCall("c1", name, args), oaiText("ok")}
	defer func() { s.replies = nil }()
	store := NewStore(t.TempDir())
	store.Save("custom", Credential{BaseURL: srv.URL})
	e, _ := Start(Options{Cwd: dir, Model: "custom/m", Mode: mode, Store: store, ConfigDir: cfgDir})
	defer e.Close()
	r := &run{t: t, e: e}
	e.Send("go")
	r.until(false) // deny any prompt: nothing leaves the machine
	asked = r.count(func(e claude.Event) bool { _, ok := e.(*claude.PermissionRequest); return ok }) > 0
	if len(s.requests) > 1 {
		result = fmt.Sprint(s.requests[1]["messages"])
	}
	return asked, result
}

func TestCredentialFilesAreOffLimitsInEveryMode(t *testing.T) {
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "auth.json"), []byte(`{"openai":{"key":"sk-secret"}}`), 0o600)
	for _, mode := range []string{"default", "auto"} {
		asked, res := safetyRun(t, mode, cfg, "Read", func(string) string {
			return `{"file_path":"` + filepath.Join(cfg, "auth.json") + `"}`
		})
		if asked || strings.Contains(res, "sk-secret") || !strings.Contains(res, "holds credentials") {
			t.Fatalf("%s: asked=%v result=%s", mode, asked, res)
		}
	}
	// A symlink in the project doesn't get around it.
	_, res := safetyRun(t, "auto", cfg, "Read", func(dir string) string {
		os.Symlink(filepath.Join(cfg, "auth.json"), filepath.Join(dir, "innocent.txt"))
		return `{"file_path":"innocent.txt"}`
	})
	if strings.Contains(res, "sk-secret") {
		t.Fatal("read credentials through a symlink")
	}
	home, _ := os.UserHomeDir()
	e := &Engine{opts: Options{ConfigDir: cfg}}
	for p, want := range map[string]bool{
		filepath.Join(home, ".ssh", "id_ed25519"):           true,
		filepath.Join(home, ".ssh", "id_ed25519.pub"):       false,
		filepath.Join(home, ".aws", "credentials"):          true,
		filepath.Join(home, ".claude", ".credentials.json"): true,
		filepath.Join(home, "projects", "x.go"):             false,
	} {
		if got := e.sensitive(p); got != want {
			t.Errorf("sensitive(%s) = %v", p, got)
		}
	}
}

func TestOutsideTheProjectAsks(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "notes.txt"), []byte("private notes"), 0o644)
	// Reads inside the project don't ask; outside they do, even for reads.
	if asked, _ := safetyRun(t, "default", "", "Read", func(dir string) string {
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
		return `{"file_path":"a.txt"}`
	}); asked {
		t.Error("asked to read inside the project")
	}
	if asked, _ := safetyRun(t, "default", "", "Read "+`{"file_path":"`+filepath.Join(outside, "notes.txt")+`"}`, nil); !asked {
		t.Error("read outside the project without asking")
	}
	if asked, _ := safetyRun(t, "default", "", "Grep "+`{"pattern":"x","path":"`+outside+`"}`, nil); !asked {
		t.Error("searched outside the project without asking")
	}
	// Accept-edits covers the project only.
	if asked, _ := safetyRun(t, "acceptEdits", "", "Write "+`{"file_path":"new.txt","content":"x"}`, nil); asked {
		t.Error("accept-edits asked about a project file")
	}
	if asked, _ := safetyRun(t, "acceptEdits", "", "Write "+`{"file_path":"`+filepath.Join(outside, "x.txt")+`","content":"x"}`, nil); !asked {
		t.Error("accept-edits wrote outside the project without asking")
	}
	// Plan mode may look things up on the web, but asks first.
	if asked, _ := safetyRun(t, "plan", "", "WebFetch "+`{"url":"https://example.com"}`, nil); !asked {
		t.Error("plan mode fetched without asking")
	}
}
