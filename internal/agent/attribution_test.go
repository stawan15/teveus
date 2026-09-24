package agent

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommitsAreTheUsersUnlessAttributionIsOn(t *testing.T) {
	for _, on := range []bool{false, true} {
		s := &script{replies: []string{oaiText("ok")}}
		srv := httptest.NewServer(s.handler(t, `{"data":[]}`))
		store := NewStore(t.TempDir())
		store.Save("custom", Credential{BaseURL: srv.URL})
		e, _ := Start(Options{Cwd: t.TempDir(), Model: "custom/m", Mode: "default", Store: store, Attribution: on})
		r := &run{t: t, e: e}
		e.Send("commit this")
		r.until(true)
		system := fmt.Sprint(s.requests[0]["messages"].([]any)[0])
		if got := strings.Contains(system, "no Co-Authored-By trailers"); got == on {
			t.Errorf("attribution=%v: prompt has the no-credit rule = %v", on, got)
		}
		e.Close()
		srv.Close()
	}
}
