package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Google's 429 for a free-tier quota, trimmed to what matters. The wait is in
// the body, and there is no Retry-After header.
const geminiQuota = `{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.\n* Quota exceeded for metric: generativelanguage.googleapis.com/generate_content_free_tier_input_token_count, limit: 250000, model: gemini-3.8-flash\nPlease retry in 1.291860953s.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_input_token_count"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1.291860953s"}]}}`

func TestRetryDelayFromBody(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(geminiQuota))}
	d, ok := retryDelayInBody(resp)
	if !ok {
		t.Fatal("no delay found")
	}
	if d < time.Second || d > 2*time.Second {
		t.Fatalf("delay = %v", d)
	}
	// The body is still readable, so apiError can report it.
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "RESOURCE_EXHAUSTED") {
		t.Error("the body was consumed")
	}

	// A top-level retryDelay works too, and a bare number of seconds.
	for body, want := range map[string]time.Duration{
		`{"retryDelay":"3s"}`: 3 * time.Second,
		`{"error":{"details":[{"@type":"x.RetryInfo","retryDelay":"7s"}]}}`: 7 * time.Second,
		`{"retryDelay":"2.5"}`: 2500 * time.Millisecond,
	} {
		r := &http.Response{StatusCode: 429, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(body))}
		if got, ok := retryDelayInBody(r); !ok || got != want {
			t.Errorf("%s: %v %v", body, got, ok)
		}
	}

	// Nothing to find is not an error.
	r := &http.Response{StatusCode: 500, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader("plain text"))}
	if _, ok := retryDelayInBody(r); ok {
		t.Error("found a delay in a plain body")
	}
}

// A quota message says what to do about it, not just that it happened, and
// stays on one line so it doesn't bury the transcript.
func TestQuotaErrorExplainsItself(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Status: "429 Too Many Requests",
		Header: http.Header{}, Body: io.NopCloser(strings.NewReader(geminiQuota))}
	got := apiError(resp).Error()
	if strings.Contains(got, "\n*") {
		t.Errorf("the quota paragraph was kept:\n%s", got)
	}
	low := strings.ToLower(got)
	for _, want := range []string{"quota", "/model", "billing"} {
		if !strings.Contains(low, want) {
			t.Errorf("%q doesn't mention %q", got, want)
		}
	}
}

// A plain 429 (busy, not a quota) keeps its old wording and its one line.
func TestPlainRateLimitStaysShort(t *testing.T) {
	resp := &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"slow down\nand wait"}}`))}
	got := apiError(resp).Error()
	if strings.Contains(got, "\n") {
		t.Errorf("multi-line: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "rate limited") {
		t.Errorf("got %q", got)
	}
}

// The retry delays have to cover a per-minute window: Google's free tier
// refills over a minute, so a fixed 19 s of waiting gives up too early.
// TestMain shrinks retryDelays so the suite runs fast, so this reads the
// shipped values from the source instead of the running ones.
func TestRetryDelaysCoverAMinute(t *testing.T) {
	src, err := os.ReadFile("httpclient.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)var retryDelays = \[\]time\.Duration\{([^}]*)\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("retryDelays not found")
	}
	var total time.Duration
	for _, part := range strings.Split(string(m[1]), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// "2 * time.Second" -> "2s": keep the number and the unit letter.
		part = strings.ReplaceAll(part, " ", "")
		part = strings.ReplaceAll(part, "*time.", "")
		part = strings.ReplaceAll(part, "Second", "s")
		part = strings.ReplaceAll(part, "Minute", "m")
		d, err := time.ParseDuration(part)
		if err != nil {
			t.Fatalf("%q: %v", part, err)
		}
		total += d
	}
	if total < 60*time.Second {
		t.Fatalf("the delays add up to %v, less than a minute", total)
	}
	if maxRetryAfter < 60*time.Second {
		t.Fatalf("the Retry-After cap is %v", maxRetryAfter)
	}
}

// The body's delay is used for the wait, so a server that asks for a minute
// gets a minute rather than the fixed two seconds.
func TestRetryHonoursTheBodyDelay(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = old }()

	var notices []time.Duration
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"details":[{"@type":"x.RetryInfo","retryDelay":"0.25s"}]}}`))
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ctx := withRetryNotice(t.Context(), func(d time.Duration, why string) { notices = append(notices, d) })
	resp, err := send(ctx, "POST", srv.URL, []byte("{}"), func(h http.Header) {})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(notices) != 1 || notices[0] != 250*time.Millisecond {
		t.Fatalf("waited %v", notices)
	}
}
