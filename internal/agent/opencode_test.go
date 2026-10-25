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

func TestOpenCodePlansUseDocumentedPathsAndSessionHeader(t *testing.T) {
	tests := []struct {
		name, base, modelsPath, chatPath string
	}{
		{"console", "", "/v1/models", "/openai/v1/chat/completions"},
		{"go", "/go", "/go/v1/models", "/go/openai/v1/chat/completions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sessions, auths, agents []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tc.modelsPath:
					io.WriteString(w, `{"data":[{"id":"mock-1"}]}`)
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
