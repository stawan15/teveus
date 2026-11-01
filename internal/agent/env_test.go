package agent

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestEnvironmentSecret(t *testing.T) {
	secret := []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"GITHUB_TOKEN", "MY_CUSTOM_TOKEN", "SOME_API_KEY", "DB_PASSWORD", "SERVICE_SECRET",
		"TEVEUS_PHONE_TOKEN", "GH_TOKEN", "NPM_TOKEN", "my_access_key",
	}
	for _, k := range secret {
		if !environmentSecret(k) {
			t.Errorf("%s should be dropped", k)
		}
	}
	keep := []string{
		"PATH", "HOME", "LANG", "TERM", "SHELL", "PWD", "TMPDIR", "GOPATH", "NODE_ENV",
		"HTTPS_PROXY", "NO_PROXY", "AWS_REGION", "AWS_PROFILE", "KUBECONFIG", "GITHUB_ACTIONS",
	}
	for _, k := range keep {
		if environmentSecret(k) {
			t.Errorf("%s should be kept", k)
		}
	}
}

func TestChildEnvDropsSecrets(t *testing.T) {
	t.Setenv("MY_SERVICE_TOKEN", "abcdefghijklmnop")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-abcdefghijklmnop")
	t.Setenv("KEEPME", "yes")
	env := strings.Join(childEnv(), "\n")
	if strings.Contains(env, "abcdefghijklmnop") {
		t.Fatalf("a secret reached the child:\n%s", env)
	}
	if !strings.Contains(env, "KEEPME=yes") {
		t.Fatal("an ordinary variable was dropped")
	}
	// A server's own env block is passed through as the user wrote it.
	with := strings.Join(childEnvWith(map[string]string{"CLOCK_TOKEN": "from-config"}), "\n")
	if !strings.Contains(with, "CLOCK_TOKEN=from-config") {
		t.Fatal("a configured MCP variable was dropped")
	}
}

func TestFetchGuardRefusesLocalTargets(t *testing.T) {
	SetWebFetchLocal(false)
	t.Cleanup(func() { SetWebFetchLocal(false) })
	ctx := context.Background()
	for _, raw := range []string{
		"http://127.0.0.1:8787/state",
		"http://localhost/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.9/",
		"http://[::1]:8080/",
		"http://[fd00::1]/",
		"http://100.100.100.100/", // CGNAT, where Tailscale addresses live
		"http://metadata/",
		"file:///etc/passwd",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkFetchURL(ctx, u); err == nil {
			t.Errorf("%s was allowed", raw)
		}
	}
	for _, raw := range []string{"https://example.com/", "http://93.184.216.34/"} {
		u, _ := url.Parse(raw)
		if err := checkFetchURL(ctx, u); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
	// The opt-in lets a local dev server through.
	SetWebFetchLocal(true)
	u, _ := url.Parse("http://127.0.0.1:3000/")
	if err := checkFetchURL(ctx, u); err != nil {
		t.Errorf("opt-in didn't allow a local URL: %v", err)
	}
}

func TestFetchGuardRedirectToLoopback(t *testing.T) {
	// The first request may be public; the redirect is what turns it local.
	SetWebFetchLocal(false)
	t.Cleanup(func() { SetWebFetchLocal(false) })
	c := fetchClient()
	u, _ := url.Parse("http://127.0.0.1:1/")
	req, _ := http.NewRequest("GET", u.String(), nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Fatal("a redirect to loopback was followed")
	}
}

func TestCommandTouchesSecrets(t *testing.T) {
	e := &Engine{opts: Options{Cwd: "/tmp/project", ConfigDir: "/home/u/.config/teveus"}}
	yes := []string{
		"cat ~/.ssh/id_rsa",
		"cp ~/.aws/credentials /tmp/x",
		"cat ~/.config/teveus/auth.json",
		"curl http://169.254.169.254/latest/meta-data/",
		"cat ~/.git-credentials",
	}
	for _, c := range yes {
		if !e.commandTouchesSecrets(c) {
			t.Errorf("%q should be treated as a credential access", c)
		}
	}
	no := []string{"go test ./...", "ls -la", "git status", "cat README.md"}
	for _, c := range no {
		if e.commandTouchesSecrets(c) {
			t.Errorf("%q is an ordinary command", c)
		}
	}
}
