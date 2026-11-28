package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A quota that clears after the wait it asks for is retried, not reported as
// a failure: this is the Gemini free-tier case.
func TestQuotaClearsAndTheTurnContinues(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	defer func() { retryDelays = old }()

	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n <= 2 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":429,"message":"You exceeded your current quota","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.01s"}]}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := newOpenAIClient(Provider{ID: "google", Protocol: OpenAI}, srv.URL, "k")
	resp, err := c.Stream(t.Context(), Request{Model: "google/gemini-x", System: "s", Messages: []Message{{Role: "user", Text: "hi"}}}, func(Chunk) {})
	if err != nil {
		t.Fatalf("gave up: %v (after %d requests)", err, n)
	}
	if resp.Text != "done" || n != 3 {
		t.Fatalf("text=%q requests=%d", resp.Text, n)
	}
}
