package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// projectInfo is what the welcome screen can learn from the folder without
// running anything in it. Detection is local and best-effort: an empty test
// command means the prompt falls back to writing tests.
type projectInfo struct {
	name     string
	language string
	test     string
	git      bool
	readme   bool
}

func defaultStarters() []string {
	return []string{
		"Explain what this project does and how it's organised",
		"Write tests for the most important function",
		"Find a bug in this code and fix it",
		"Review my uncommitted changes",
	}
}

func (m *Model) setProject(cwd string) {
	m.project = detectProject(cwd)
	m.starters = starterPrompts(m.project)
}

func (m *Model) starterList() []string {
	if len(m.starters) > 0 {
		return m.starters
	}
	return defaultStarters()
}

func starterPrompts(p projectInfo) []string {
	explain := "Explain what this project does and how it's organised"
	if p.language != "" {
		explain = "Explain what this " + p.language + " project does and how it's organised"
	}
	tests := "Write tests for the most important function"
	if p.test != "" {
		tests = "Run " + p.test + " and fix any failures"
	}
	last := "Review my uncommitted changes"
	if !p.git {
		last = "Explain the folder structure and where to start"
		if p.readme {
			last = "Read the README and tell me how to run this project"
		}
	}
	return []string{explain, tests, "Find a bug in this code and fix it", last}
}

func detectProject(cwd string) projectInfo {
	p := projectInfo{name: filepath.Base(cwd)}
	p.git = pathExists(cwd, ".git")
	for _, name := range []string{"README.md", "README", "README.rst", "README.txt"} {
		if pathExists(cwd, name) {
			p.readme = true
			break
		}
	}

	switch {
	case pathExists(cwd, "go.mod"):
		p.language, p.test = "Go", "go test ./..."
	case pathExists(cwd, "Cargo.toml"):
		p.language, p.test = "Rust", "cargo test"
	case pathExists(cwd, "package.json"):
		p.language = "JavaScript"
		if pathExists(cwd, "tsconfig.json") {
			p.language = "TypeScript"
		}
		if packageHasTest(cwd) {
			p.test = nodeTest(cwd)
		}
	case pathExists(cwd, "pyproject.toml") || pathExists(cwd, "requirements.txt") || pathExists(cwd, "setup.py"):
		p.language, p.test = "Python", pythonTest(cwd)
	case pathExists(cwd, "Gemfile") || pathExists(cwd, "Rakefile"):
		p.language = "Ruby"
		if pathExists(cwd, "spec") {
			p.test = "bundle exec rspec"
		} else if pathExists(cwd, "Rakefile") {
			p.test = "bundle exec rake test"
		}
	case pathExists(cwd, "composer.json"):
		p.language = "PHP"
		if hasJSONTestScript(filepath.Join(cwd, "composer.json")) {
			p.test = "composer test"
		}
	case pathExists(cwd, "mix.exs"):
		p.language, p.test = "Elixir", "mix test"
	case pathExists(cwd, "Package.swift"):
		p.language, p.test = "Swift", "swift test"
	case pathExists(cwd, "pom.xml"):
		p.language, p.test = "Java", "mvn test"
	case pathExists(cwd, "gradlew"):
		p.language, p.test = "Java", "./gradlew test"
	case pathExists(cwd, "build.gradle") || pathExists(cwd, "build.gradle.kts"):
		p.language, p.test = "Java", "gradle test"
	}
	if p.test == "" && makeHasTest(cwd) {
		p.test = "make test"
	}
	return p
}

func pathExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func packageHasTest(cwd string) bool {
	return hasJSONTestScript(filepath.Join(cwd, "package.json"))
}

func hasJSONTestScript(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	test := strings.ToLower(strings.TrimSpace(pkg.Scripts["test"]))
	return test != "" && !strings.Contains(test, "no test specified")
}

func nodeTest(cwd string) string {
	switch {
	case pathExists(cwd, "pnpm-lock.yaml"):
		return "pnpm test"
	case pathExists(cwd, "yarn.lock"):
		return "yarn test"
	case pathExists(cwd, "bun.lockb") || pathExists(cwd, "bun.lock"):
		return "bun run test"
	default:
		return "npm test"
	}
}

func pythonTest(cwd string) string {
	found := false
	for _, name := range []string{"pyproject.toml", "requirements.txt", "requirements-dev.txt", "setup.cfg", "tox.ini"} {
		b, err := os.ReadFile(filepath.Join(cwd, name))
		if err == nil && strings.Contains(strings.ToLower(string(b)), "pytest") {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	switch {
	case pathExists(cwd, "uv.lock"):
		return "uv run pytest"
	case pathExists(cwd, "poetry.lock"):
		return "poetry run pytest"
	default:
		return "pytest"
	}
}

func makeHasTest(cwd string) bool {
	b, err := os.ReadFile(filepath.Join(cwd, "Makefile"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "test:") {
			return true
		}
	}
	return false
}
