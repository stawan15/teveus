package agent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestOpenCodeUsageReportsPlanWindows(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path + " " + r.Header.Get("Authorization")
		io.WriteString(w, `{"usage":{`+
			`"rolling":{"status":"ok","percent":25,"resetsAt":"2026-10-25T15:07:59.000Z"},`+
			`"weekly":{"status":"ok","percent":2,"resetsAt":"2026-10-26T00:00:00.000Z"},`+
			`"monthly":{"status":"ok","percent":1,"resetsAt":"2026-11-25T10:05:24.000Z"}}}`)
	}))
	defer srv.Close()

	rl, err := openCodeUsage(t.Context(), srv.URL+"/go", "oc_sk_test")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if asked != "/go/v1/usage Bearer oc_sk_test" {
		t.Fatalf("request = %q", asked)
	}
	if rl == nil || rl.FiveHour == nil || rl.SevenDay == nil || rl.Monthly == nil {
		t.Fatalf("windows = %+v", rl)
	}
	if rl.FiveHour.Utilization != 0.25 || rl.SevenDay.Utilization != 0.02 || rl.Monthly.Utilization != 0.01 {
		t.Fatalf("utilization = %v/%v/%v", rl.FiveHour.Utilization, rl.SevenDay.Utilization, rl.Monthly.Utilization)
	}
	if rl.FiveHour.ResetsAt == 0 {
		t.Fatal("rolling window has no reset time")
	}

	// The Console plan has no Go allowance to report.
	rl, err = openCodeUsage(t.Context(), srv.URL, "oc_sk_test")
	if err != nil || rl != nil {
		t.Fatalf("console usage = %+v, %v; want none", rl, err)
	}
}

func TestOpenCodePlansUseDocumentedPathsAndSessionHeader(t *testing.T) {
	tests := []struct {
		name, base, modelsPath, chatPath, usagePath string
	}{
		{"console", "", "/v1/models", "/openai/v1/chat/completions", ""},
		{"go", "/go", "/go/v1/models", "/go/openai/v1/chat/completions", "/go/v1/usage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sessions, auths, agents []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tc.modelsPath:
					io.WriteString(w, `{"data":[{"id":"mock-1"}]}`)
				case tc.usagePath:
					io.WriteString(w, `{"usage":{`+
						`"rolling":{"status":"ok","percent":25,"resetsAt":"2026-10-25T15:07:59.000Z"},`+
						`"weekly":{"status":"ok","percent":2,"resetsAt":"2026-10-26T00:00:00.000Z"},`+
						`"monthly":{"status":"ok","percent":1,"resetsAt":"2026-11-25T10:05:24.000Z"}}}`)
				case tc.chatPath:
					mu.Lock()
					sessions = append(sessions, r.Header.Get("x-opencode-session"))
					auths = append(auths, r.Header.Get("Authorization"))
					agents = append(agents, r.Header.Get("User-Agent"))
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, oaiText("ok"))
				default:
					t.Errorf("unexpected path %q", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			base := srv.URL + tc.base
			r, _ := startEngine(t, "opencode", Credential{Key: "oc_sk_test", BaseURL: base}, "opencode/mock-1", "default")
			for _, prompt := range []string{"one", "two"} {
				r.e.Send(prompt)
				if res := r.until(true); res.IsError {
					t.Fatalf("result = %+v", res)
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if len(sessions) != 2 {
				t.Fatalf("chat requests = %d, want 2", len(sessions))
			}
			if sessions[0] == "" || !strings.HasPrefix(sessions[0], "ses_") {
				t.Fatalf("session header = %q", sessions[0])
			}
			if sessions[1] != sessions[0] {
				t.Fatalf("session header changed: %q then %q", sessions[0], sessions[1])
			}
			for i := range auths {
				if auths[i] != "Bearer oc_sk_test" {
					t.Fatalf("authorization = %q", auths[i])
				}
				if agents[i] != userAgent {
					t.Fatalf("user agent = %q", agents[i])
				}
			}
		})
	}
}
