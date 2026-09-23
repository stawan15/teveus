package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

func TestHeadlessRun(t *testing.T) {
	for _, v := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(v, "")
	}
	for _, mode := range []string{"acceptEdits", "default"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TEVEUS_CONFIG", t.TempDir())
			srv := mockProvider(t)
			defer srv.Close()
			agent.NewStore(configDir()).Save("custom", agent.Credential{Key: "sk-test", BaseURL: srv.URL})
			dir := t.TempDir()
			var out, errs bytes.Buffer
			cfg := Config{Claude: claude.Options{Cwd: dir, Model: "custom/coder-large", PermissionMode: mode}, Engine: "api"}
			if err := RunHeadless(cfg, "make hello.txt", true, &out, &errs); err != nil {
				t.Fatalf("%v (%s)", err, errs.String())
			}
			var res HeadlessResult
			if err := json.Unmarshal(out.Bytes(), &res); err != nil {
				t.Fatalf("%v: %s", err, out.String())
			}
			_, err := os.Stat(filepath.Join(dir, "hello.txt"))
			if mode == "acceptEdits" {
				if err != nil || res.Result != "Created **hello.txt**." || res.InputTokens != 2500 || res.OutputTokens != 48 || res.Turns != 2 || res.Engine != "api" {
					t.Fatalf("result %+v (file: %v)", res, err)
				}
				return
			}
			// Unapproved tools are denied, which ends the turn.
			if err == nil || len(res.Denied) != 1 || !strings.Contains(errs.String(), "denied Write") || res.Turns != 1 {
				t.Fatalf("default mode: %+v", res)
			}
		})
	}
}
