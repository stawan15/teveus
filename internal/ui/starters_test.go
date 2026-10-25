package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectProjectTestCommands(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"go", map[string]string{"go.mod": "module x\n"}, "go test ./..."},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"}, "cargo test"},
		{"npm", map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`}, "npm test"},
		{"pnpm", map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`, "pnpm-lock.yaml": ""}, "pnpm test"},
		{"yarn", map[string]string{"package.json": `{"scripts":{"test":"vitest"}}`, "yarn.lock": ""}, "yarn test"},
		{"bun", map[string]string{"package.json": `{"scripts":{"test":"bun test"}}`, "bun.lockb": ""}, "bun run test"},
		{"pytest", map[string]string{"pyproject.toml": "dependencies = [\"pytest\"]\n"}, "pytest"},
		{"uv", map[string]string{"pyproject.toml": "dependencies = [\"pytest\"]\n", "uv.lock": ""}, "uv run pytest"},
		{"make", map[string]string{"Makefile": "build:\n\ttrue\n\ntest:\n\tgo test ./...\n"}, "make test"},
		{"placeholder npm test", map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range c.files {
				writeFile(t, dir, name, body)
			}
			if got := detectProject(dir).test; got != c.want {
				t.Fatalf("test command = %q, want %q", got, c.want)
			}
		})
	}
}

func TestProjectStarters(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")

	prompts := starterPrompts(detectProject(dir))
	want := []string{
		"Explain what this Go project does and how it's organised",
		"Run go test ./... and fix any failures",
		"Find a bug in this code and fix it",
		"Review my uncommitted changes",
	}
	if strings.Join(prompts, "|") != strings.Join(want, "|") {
		t.Fatalf("starters = %q", prompts)
	}
}

func TestProjectStartersWithoutGit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\n")
	writeFile(t, dir, "README.md", "# demo\n")

	prompts := starterPrompts(detectProject(dir))
	if prompts[0] != "Explain what this Rust project does and how it's organised" {
		t.Fatalf("explain = %q", prompts[0])
	}
	if prompts[3] != "Read the README and tell me how to run this project" {
		t.Fatalf("last = %q", prompts[3])
	}
}

func TestWelcomeUsesProjectStarters(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n")

	m := New(Config{Claude: claude.Options{Cwd: dir}, Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	if !strings.Contains(plain(m.View()), "Run go test ./... and fix any failures") {
		t.Fatalf("welcome did not show the project starter:\n%s", plain(m.View()))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if got := m.input.Value(); got != "Run go test ./... and fix any failures" {
		t.Fatalf("starter 2 = %q", got)
	}
}
