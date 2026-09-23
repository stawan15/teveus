package agent

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stawan15/teveus/internal/claude"
)

func TestRetriesBusyProviders(t *testing.T) {
	s := &script{replies: []string{"HTTP429 slow down", "HTTP503 overloaded", oaiText("finally")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("hi")
	if res := r.until(true); res.IsError {
		t.Fatalf("result %+v", res)
	}
	retries := r.count(func(e claude.Event) bool {
		st, ok := e.(claude.Status)
		return ok && strings.HasPrefix(st.Status, "retrying")
	})
	if len(s.requests) != 3 || retries != 2 {
		t.Fatalf("requests=%d retry notices=%d", len(s.requests), retries)
	}
}

func TestGivesUpAfterRetriesAndNeverRetriesClientErrors(t *testing.T) {
	s := &script{replies: []string{"HTTP500 a", "HTTP500 b", "HTTP500 c", "HTTP500 d"}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "default")
	r.e.Send("hi")
	if res := r.until(true); !res.IsError || len(s.requests) != 4 {
		t.Fatalf("result %+v after %d requests", res, len(s.requests))
	}

	s2 := &script{replies: []string{"HTTP400 bad request"}}
	srv2 := httptest.NewServer(s2.handler(t, `{"data":[]}`))
	defer srv2.Close()
	r2, _ := startEngine(t, "custom", Credential{BaseURL: srv2.URL}, "custom/m", "default")
	r2.e.Send("hi")
	if res := r2.until(true); !res.IsError || len(s2.requests) != 1 {
		t.Fatalf("400 was retried: %d requests", len(s2.requests))
	}
}

func TestCutOffToolCallsAreNotRun(t *testing.T) {
	cut := strings.Replace(oaiToolCall("c1", "Write", `{"file_path":"x.txt","content":"half`), `"finish_reason":"tool_calls"`, `"finish_reason":"length"`, 1)
	s := &script{replies: []string{cut, oaiText("ok, smaller")}}
	srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
	defer srv.Close()
	r, _ := startEngine(t, "custom", Credential{BaseURL: srv.URL}, "custom/m", "auto")
	r.e.Send("write x")
	r.until(true)
	if !strings.Contains(fmt.Sprint(s.requests[1]["messages"]), "reached the output limit") {
		t.Fatal("model wasn't told the call was cut off")
	}
}
