package agent

import (
	"os"
	"strings"
)

// Child processes — Bash, background jobs, hooks, MCP servers — used to get
// the whole environment, including every credential the user's shell exports.
// A model that can run `env` could then read them and, through a model
// provider, send them away. childEnv keeps the environment a command actually
// needs (PATH, HOME, locale, proxies) and drops the variables that hold keys.

// secretEnvExact are variables that are secrets on their own.
var secretEnvExact = map[string]bool{
	"AWS_SECRET_ACCESS_KEY":   true,
	"AWS_SESSION_TOKEN":       true,
	"ANTHROPIC_API_KEY":       true,
	"OPENAI_API_KEY":          true,
	"OPENROUTER_API_KEY":      true,
	"GOOGLE_API_KEY":          true,
	"GEMINI_API_KEY":          true,
	"BRAVE_API_KEY":           true,
	"TAVILY_API_KEY":          true,
	"GROQ_API_KEY":            true,
	"MISTRAL_API_KEY":         true,
	"XAI_API_KEY":             true,
	"DEEPSEEK_API_KEY":        true,
	"TOGETHER_API_KEY":        true,
	"FIREWORKS_API_KEY":       true,
	"CEREBRAS_API_KEY":        true,
	"PERPLEXITY_API_KEY":      true,
	"GITHUB_TOKEN":            true,
	"GH_TOKEN":                true,
	"GITLAB_TOKEN":            true,
	"NPM_TOKEN":               true,
	"PYPI_TOKEN":              true,
	"CARGO_REGISTRY_TOKEN":    true,
	"DOCKER_PASSWORD":         true,
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	"TEVEUS_PHONE_TOKEN":      true,
	"SSH_AUTH_SOCK":           true,
}

// secretEnvSuffix drops any variable whose name ends in one of these, in any
// case: ${SOMETHING}_API_KEY, *_TOKEN, *_SECRET, *_PASSWORD.
var secretEnvSuffix = []string{
	"_API_KEY", "_APIKEY", "_ACCESS_KEY", "_SECRET_KEY", "_SECRET", "_TOKEN",
	"_PASSWORD", "_PASSWD", "_CREDENTIALS", "_PRIVATE_KEY",
}

// secretEnvPrefix drops whole families at once.
var secretEnvPrefix = []string{
	"AWS_", "OPENAI_", "ANTHROPIC_", "GOOGLE_", "GEMINI_", "AZURE_",
	"GITHUB_", "GITLAB_", "TEVEUS_", "NPM_", "DOCKER_", "KUBE",
}

// envAllowed is checked before the drop rules: a few variables look like
// secrets by name but are needed to reach the network or the filesystem.
var envAllowed = map[string]bool{
	"AWS_REGION":                  true,
	"AWS_DEFAULT_REGION":          true,
	"AWS_PROFILE":                 true,
	"GOOGLE_CLOUD_PROJECT":        true,
	"AWS_VAULT":                   true,
	"GITHUB_ACTIONS":              true,
	"GITHUB_WORKSPACE":            true,
	"GITHUB_REPOSITORY":           true,
	"KUBECONFIG":                  true,
	"AWS_CONFIG_FILE":             true,
	"AWS_SHARED_CREDENTIALS_FILE": true,
}

func environmentSecret(name string) bool {
	up := strings.ToUpper(name)
	if envAllowed[up] {
		return false
	}
	if secretEnvExact[up] {
		return true
	}
	for _, p := range secretEnvPrefix {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	for _, s := range secretEnvSuffix {
		if strings.HasSuffix(up, s) {
			return true
		}
	}
	return false
}

// childEnv is os.Environ without the credential variables.
func childEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if environmentSecret(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// childEnvWith is childEnv plus the variables a specific tool was configured
// with (an MCP server's env block). Those are the user's choice, so they are
// passed through as written.
func childEnvWith(extra map[string]string) []string {
	out := childEnv()
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
