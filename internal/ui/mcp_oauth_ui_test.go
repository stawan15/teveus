package ui

import (
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
)

// An OAuth server offers Sign in, not "add a token": a pasted token can't
// satisfy a browser sign-in, and offering both wastes the user's time.
func TestMCPOAuthOffersSignIn(t *testing.T) {
	m := New(Config{Dark: true})
	cs := m.mcpChoices(nil, []agent.MCPServerStatus{
		{Name: "pastel", Scope: "user", State: "initialize: 401 Unauthorized: needs authorization",
			NeedsOAuth: true, OAuthURL: "https://x/.well-known/oauth-protected-resource"},
	})
	var labels []string
	for _, c := range cs {
		labels = append(labels, c.label)
	}
	got := strings.Join(labels, " | ")
	if !strings.Contains(got, "Sign in to pastel") {
		t.Errorf("no sign-in option: %s", got)
	}
	if strings.Contains(got, "Add a token for pastel") {
		t.Errorf("offered a token prompt for an OAuth server: %s", got)
	}
}

// A plain 401 (no OAuth challenge) keeps the token prompt.
func TestMCPPlain401StillOffersToken(t *testing.T) {
	m := New(Config{Dark: true})
	cs := m.mcpChoices(nil, []agent.MCPServerStatus{
		{Name: "api", Scope: "user", State: "initialize: 401 Unauthorized: needs authorization"},
	})
	var labels []string
	for _, c := range cs {
		labels = append(labels, c.label)
	}
	got := strings.Join(labels, " | ")
	if !strings.Contains(got, "Add a token for api") || strings.Contains(got, "Sign in") {
		t.Errorf("labels = %s", got)
	}
}
