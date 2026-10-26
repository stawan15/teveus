package ui

import (
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/agent"
)

func TestOpenLoginListsEveryProvider(t *testing.T) {
	// The login screen is how anyone connects, so every provider and the
	// search services have to be on it, each with a description.
	m := testModel(t)
	m.openLogin("")
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	byValue := map[string]choice{}
	for _, c := range m.pop.list {
		byValue[c.value] = c
		if c.desc == "" {
			t.Errorf("%q has no description", c.label)
		}
	}
	if _, ok := byValue["claude"]; !ok {
		t.Error("Claude Code isn't offered")
	}
	for _, p := range agent.Providers {
		if _, ok := byValue[p.ID]; !ok {
			t.Errorf("%s is missing", p.ID)
		}
	}
	for _, p := range agent.SearchProviders {
		if _, ok := byValue[p.ID]; !ok {
			t.Errorf("search provider %s is missing", p.ID)
		}
	}
}

func TestOpenLoginWithANameGoesStraightToIt(t *testing.T) {
	m := testModel(t)
	for _, p := range agent.Providers {
		m2 := testModel(t)
		if _, ok := agent.ProviderByID(p.ID); !ok {
			continue
		}
		m2.openLogin(p.ID)
		// A local server needs nothing from the user, so it is checked
		// instead of asking a question.
		if p.NoKey {
			if m2.pop.open() {
				t.Errorf("/login %s asked for something a keyless server doesn't need", p.ID)
			}
			continue
		}
		if !m2.pop.open() {
			t.Errorf("/login %s opened nothing", p.ID)
		}
	}
	// A search provider asks for its key.
	m.openLogin("brave")
	if m.pop.mode != popInput {
		t.Fatalf("brave login = %v, want a key field", m.pop.mode)
	}
}

func TestLoginProviderAsksForWhatItNeeds(t *testing.T) {
	m := testModel(t)

	// A local provider needs no key: it is checked straight away.
	local := agent.Provider{ID: "local", Name: "Local", NoKey: true}
	if cmd := m.loginProvider(local); cmd == nil {
		t.Fatal("a local provider wasn't checked")
	}

	// A custom provider asks for a base URL first.
	custom := agent.Provider{ID: "custom", Name: "Custom", Custom: true}
	m.loginProvider(custom)
	if m.pop.mode != popInput || !strings.Contains(m.pop.title, "base URL") {
		t.Fatalf("custom login = %v %q", m.pop.mode, m.pop.title)
	}

	// OpenRouter offers the browser, and a key as the fallback.
	browser := agent.Provider{ID: "openrouter", Name: "OpenRouter", Browser: true}
	m.loginProvider(browser)
	if m.pop.mode != popPicker || len(m.pop.list) != 2 {
		t.Fatalf("browser login = %v with %d rows", m.pop.mode, len(m.pop.list))
	}

	// OpenCode asks which plan, because the two use different base URLs.
	oc := agent.Provider{ID: "opencode", Name: "OpenCode"}
	m.loginProvider(oc)
	if m.pop.mode != popPicker || len(m.pop.list) != 2 {
		t.Fatalf("opencode login = %v with %d rows", m.pop.mode, len(m.pop.list))
	}

	// Anything else asks for an API key, and says where to get one.
	keyed := agent.Provider{ID: "openai", Name: "OpenAI", KeyURL: "https://platform.openai.com/keys"}
	m.loginProvider(keyed)
	if m.pop.mode != popInput || !m.pop.masked {
		t.Fatalf("key login = %v masked=%v", m.pop.mode, m.pop.masked)
	}
	if !strings.Contains(m.pop.hint, "platform.openai.com") {
		t.Fatalf("hint = %q, want the key URL", m.pop.hint)
	}
}
