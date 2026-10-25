package ui

import (
	"testing"

	"github.com/stawan15/teveus/internal/agent"
)

func TestOpenCodeLoginOffersBothPlans(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.openLogin("opencode")
	if m.pop.mode != popPicker || m.pop.title != "OpenCode plan" {
		t.Fatalf("popup = %q (mode %d)", m.pop.title, m.pop.mode)
	}
	var labels []string
	for _, c := range m.pop.all {
		labels = append(labels, c.label)
	}
	if len(labels) != 2 || labels[0] != "OpenCode Go" || labels[1] != "OpenCode Console" {
		t.Fatalf("plans = %v", labels)
	}
	m.pop.all[0].run(m)
	if m.pop.mode != popInput || m.pop.title != "OpenCode Go API key" {
		t.Fatalf("after picking Go: %q (mode %d)", m.pop.title, m.pop.mode)
	}
}

func TestOpenCodeStatusShowsSavedPlan(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	p, ok := agent.ProviderByID("opencode")
	if !ok {
		t.Fatal("opencode provider missing")
	}
	s := agent.NewStore(configDir())
	if err := s.Save("opencode", agent.Credential{Key: "k", BaseURL: "https://opencode.ai/inference/go"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := providerStatus(s, p); got != "✓ OpenCode Go" {
		t.Fatalf("status = %q", got)
	}
}
