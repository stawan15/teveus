package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"
)

// openCodeModels is the shape OpenCode's inference gateway returns (copied from
// a live reply): limits and prices are nested, and the effort levels a model
// takes are listed, unlike OpenRouter's flat context_length.
const openCodeModels = `{"object":"list","data":[
{"id":"deepseek-v4.1-flash","object":"model","owned_by":"opencode","name":"DeepSeek V4.1 Flash",
 "created":1793509091,
 "capabilities":{"tools":{"supported":true},"reasoning":{"supported":true,"toggle":true,"effort":["low","high","max"]}},
 "limit":{"context":1000000,"output":384000},
 "cost":{"text":{"input":{"price":0.3,"cache":{"read":0.006}},"output":{"price":1.2}}}},
{"id":"big-pickle","object":"model","owned_by":"opencode","name":"Big Pickle",
 "created":1793509091,
 "capabilities":{"reasoning":{"supported":true}},
 "limit":{"context":200000,"output":32000},
 "cost":{"text":{"input":{"price":0,"cache":{"read":0}},"output":{"price":0}}}}
]}`

func TestModelsReadsGatewaysThatNestFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(openCodeModels))
	}))
	defer srv.Close()
	c := NewClient(Provider{ID: "opencode", Protocol: OpenAI, BaseURL: srv.URL, ModelsPath: "/models"}, Credential{Key: "k"})
	ms, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Model{}
	for _, m := range ms {
		byID[m.ID] = m
	}

	ds := byID["deepseek-v4.1-flash"]
	// Without the window, auto-compaction never triggers: it is gated on
	// knowing the limit.
	if ds.Context != 1000000 {
		t.Errorf("context = %d, want 1000000", ds.Context)
	}
	if !ds.Priced || ds.In != 0.3 || ds.Out != 1.2 {
		t.Errorf("prices = %v/%v priced=%v, want 0.3/1.2", ds.In, ds.Out, ds.Priced)
	}
	if len(ds.Efforts) != 3 || ds.Efforts[2] != "max" {
		t.Errorf("efforts = %v", ds.Efforts)
	}

	// A free model is still marked free, not unpriced.
	if pk := byID["big-pickle"]; !pk.Priced || pk.In != 0 || pk.Out != 0 {
		t.Errorf("big-pickle = %+v", pk)
	}
}

// The provider's own list beats the protocol's ceiling: OpenCode passes max
// through to a model that takes it, while OpenRouter-shaped replies keep using
// the OpenAI ceiling.
func TestEffortUsesTheModelListWhenPublished(t *testing.T) {
	levels := []string{"low", "high", "max"}
	if got, capped := ClampToModel("max", "opencode/deepseek-v4.1-flash", levels); got != "max" || capped {
		t.Errorf("max against a model that takes it = %q capped=%v", got, capped)
	}
	// A level the model doesn't list drops to the highest one it does that is
	// still below what was asked; max is above xhigh, so high is the answer.
	if got, capped := ClampToModel("xhigh", "opencode/deepseek-v4.1-flash", levels); got != "high" || !capped {
		t.Errorf("xhigh = %q capped=%v, want high", got, capped)
	}
	// No list published: fall back to the protocol.
	if got, capped := ClampToModel("max", "opencode/some-model", nil); got != "high" || !capped {
		t.Errorf("max without a list = %q capped=%v, want high", got, capped)
	}
	// The request builder must not clamp away what the engine allowed.
	if got := openaiEffort("max"); got != "max" {
		t.Errorf("openaiEffort(max) = %q, which would drop the level the model takes", got)
	}
}

// The window has to reach contextLimit, or compaction is dead for the model.
func TestGatewayContextWindowEnablesCompaction(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(t.TempDir())
	store.Save("custom", Credential{Key: "k", BaseURL: testModelServer(t).URL})
	e, _ := Start(Options{Cwd: dir, Model: "custom/deepseek-v4.1-flash", Mode: "default", Store: store})
	t.Cleanup(e.Close)
	// The model list is fetched in the background at startup.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e.contextLimit() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := e.contextLimit(); got != 1000000 {
		t.Fatalf("contextLimit = %d, want 1000000: auto-compaction would never fire", got)
	}
	if got := e.efforts["custom/deepseek-v4.1-flash"]; len(got) != 3 {
		t.Fatalf("efforts = %v, want the published levels", got)
	}
}

func testModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(openCodeModels))
	}))
	t.Cleanup(srv.Close)
	return srv
}
