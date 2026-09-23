package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
	"github.com/stawan15/teveus/internal/claude"
)

func TestSavedAPIEngineWithoutProvidersFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", dir)
	for _, p := range agent.Providers {
		for _, env := range p.EnvKeys {
			t.Setenv(env, "")
		}
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{}"), 0o600)
	m := New(Config{Claude: claude.Options{Cwd: dir}, Settings: Settings{Engine: "api"}})
	if m.engine != "claude" {
		t.Fatalf("engine = %q, want claude", m.engine)
	}
	// An explicit -engine api is respected.
	if m := New(Config{Claude: claude.Options{Cwd: dir}, Engine: "api"}); m.engine != "api" {
		t.Fatalf("explicit engine = %q", m.engine)
	}
	// With a saved key the saved API engine is kept.
	agent.NewStore(dir).Save("openrouter", agent.Credential{Key: "k"})
	if m := New(Config{Claude: claude.Options{Cwd: dir}, Settings: Settings{Engine: "api"}}); m.engine != "api" {
		t.Fatalf("with key engine = %q", m.engine)
	}
}
