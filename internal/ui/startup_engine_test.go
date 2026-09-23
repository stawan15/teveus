package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

func TestStartsDisconnectedUntilAnEngineIsChosen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", dir)
	for _, p := range agent.Providers {
		for _, env := range p.EnvKeys {
			t.Setenv(env, "")
		}
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600)
	opts := claude.Options{Cwd: dir}
	for name, c := range map[string]struct {
		cfg  Config
		want string
	}{
		"first run":                      {Config{Claude: opts}, ""},
		"saved API engine, no key":       {Config{Claude: opts, Settings: Settings{Engine: "api"}}, ""},
		"saved Claude, never agreed":     {Config{Claude: opts, Settings: Settings{Engine: "claude"}}, ""},
		"saved Claude, agreed":           {Config{Claude: opts, Settings: Settings{Engine: "claude", ClaudeNotice: true}}, "claude"},
		"-engine api":                    {Config{Claude: opts, Engine: "api"}, "api"},
		"-engine claude (asks at start)": {Config{Claude: opts, Engine: "claude"}, "claude"},
	} {
		if m := New(c.cfg); m.engine != c.want {
			t.Errorf("%s: engine = %q, want %q", name, m.engine, c.want)
		}
	}
	// With a saved key the saved API engine is kept.
	agent.NewStore(dir).Save("openrouter", agent.Credential{Key: "k"})
	if m := New(Config{Claude: opts, Settings: Settings{Engine: "api"}}); m.engine != "api" {
		t.Fatalf("with key engine = %q", m.engine)
	}
}
