package claude

import (
	"strings"
	"testing"
	"time"
)

func TestStartBuildsTheDocumentedArguments(t *testing.T) {
	// Only the CLI's documented flags may be passed, so this pins them.
	f := newFakeCLI(t)
	c := f.start(Options{
		Model: "opus", PermissionMode: "acceptEdits", Resume: "ses-1",
		AppendPrompt: "be brief", DisallowTools: []string{"WebSearch", "WebFetch"},
		Effort: "high", NoAttribution: true, ExtraArgs: []string{"first"},
	})
	_ = c

	args := strings.Join(f.args(), " ")
	for _, want := range []string{
		"-p", "--input-format stream-json", "--output-format stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool stdio",
		"--model opus", "--permission-mode acceptEdits", "--resume ses-1",
		"--append-system-prompt be brief", "--disallowedTools WebSearch,WebFetch",
		"--effort high", `--settings {"attribution":{"commit":"","pr":""}}`,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("arguments are missing %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "--continue") {
		t.Errorf("--resume and --continue were both passed:\n%s", args)
	}
}

func TestStartContinueWithoutResume(t *testing.T) {
	f := newFakeCLI(t)
	f.start(Options{Continue: true})
	if args := strings.Join(f.args(), " "); !strings.Contains(args, "--continue") {
		t.Fatalf("--continue missing:\n%s", args)
	}
}

func TestStartReportsAMissingBinary(t *testing.T) {
	// The error has to name the binary, or the user can't tell what's wrong.
	_, err := Start(Options{Binary: "/nonexistent/claude-xyz"})
	if err == nil {
		t.Fatal("starting a missing binary succeeded")
	}
	if !strings.Contains(err.Error(), "claude-xyz") {
		t.Fatalf("error doesn't name the binary: %v", err)
	}
}

func TestSendWritesAUserTurn(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	if err := c.Send("hello there"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, func() bool { return len(f.told()) >= 2 }) // init, then the turn
	turns := f.told()
	last := turns[len(turns)-1]
	if last["type"] != "user" {
		t.Fatalf("type = %v", last["type"])
	}
	msg, _ := last["message"].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "hello there" {
		t.Fatalf("message = %v", msg)
	}
}

func TestSendImagesBase64EncodesPicturesFirst(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	err := c.SendImages("look", []Image{{MediaType: "image/png", Data: []byte("png!")}})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, func() bool {
		turns := f.told()
		return len(turns) >= 2 && turns[len(turns)-1]["type"] == "user"
	})
	last := f.told()[1]
	msg := last["message"].(map[string]any)
	parts := msg["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("content = %v", parts)
	}
	// The picture comes before the text, as the API recommends.
	img := parts[0].(map[string]any)
	if img["type"] != "image" {
		t.Fatalf("first part = %v", img)
	}
	src := img["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "cG5nIQ==" {
		t.Fatalf("source = %v", src)
	}
	if text := parts[1].(map[string]any); text["type"] != "text" || text["text"] != "look" {
		t.Fatalf("second part = %v", text)
	}
}

func TestControlRequestsCarryTaggedIDs(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPermissionMode("plan"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetModel("sonnet"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		told := f.told()
		var controls int
		for _, m := range told {
			if m["type"] == "control_request" {
				controls++
			}
		}
		return controls >= 4 // the init plus these three
	})

	var ids []string
	var subtypes []string
	for _, m := range f.told() {
		if m["type"] != "control_request" {
			continue
		}
		id, _ := m["request_id"].(string)
		ids = append(ids, id)
		req, _ := m["request"].(map[string]any)
		subtypes = append(subtypes, req["subtype"].(string))
	}
	// The tag prefixes the ID so a ControlResult can be attributed.
	joined := strings.Join(ids, " ")
	for _, want := range []string{"init-", "req-", "mode-", "model-"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no request id tagged %q: %v", want, ids)
		}
	}
	if got := strings.Join(subtypes, " "); !strings.Contains(got, "interrupt") ||
		!strings.Contains(got, "set_permission_mode") || !strings.Contains(got, "set_model") {
		t.Errorf("subtypes = %v", subtypes)
	}
	// The mode travels with its request.
	for _, m := range f.told() {
		req, _ := m["request"].(map[string]any)
		if req != nil && req["subtype"] == "set_permission_mode" && req["mode"] != "plan" {
			t.Errorf("mode not sent: %v", req)
		}
	}
}

func TestAllowAndDenyAnswerTheRequest(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	req := &PermissionRequest{RequestID: "tu-1", Input: []byte(`{"command":"ls"}`)}

	if err := c.Allow(req, false); err != nil {
		t.Fatal(err)
	}
	// With suggestions and always, the CLI's own rules are applied.
	req2 := &PermissionRequest{RequestID: "tu-2", Input: []byte(`{}`),
		Suggestions: []byte(`[{"type":"allow"}]`)}
	if err := c.Allow(req2, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Allow(req2, false); err != nil { // suggestions but not always
		t.Fatal(err)
	}
	if err := c.Deny(req, "not now"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var n int
		for _, m := range f.told() {
			if m["type"] == "control_response" {
				n++
			}
		}
		return n >= 4
	})

	var allows, denies, withPerms int
	var denyMsg string
	for _, m := range f.told() {
		if m["type"] != "control_response" {
			continue
		}
		resp := m["response"].(map[string]any)
		inner := resp["response"].(map[string]any)
		switch inner["behavior"] {
		case "allow":
			allows++
			if _, ok := inner["updatedPermissions"]; ok {
				withPerms++
			}
		case "deny":
			denies++
			denyMsg, _ = inner["message"].(string)
		}
		if resp["subtype"] != "success" {
			t.Errorf("subtype = %v", resp["subtype"])
		}
	}
	if allows != 3 || denies != 1 {
		t.Fatalf("allows=%d denies=%d", allows, denies)
	}
	if withPerms != 1 {
		t.Fatalf("%d responses carried suggestions, want 1 (always only)", withPerms)
	}
	if denyMsg != "not now" {
		t.Fatalf("deny message = %q", denyMsg)
	}
}

func TestRespondErrorWritesAnErrorResponse(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	if err := c.respondError("tu-9", "no such tool"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, m := range f.told() {
			if m["type"] == "control_response" {
				resp := m["response"].(map[string]any)
				if resp["subtype"] == "error" && resp["error"] == "no such tool" {
					return true
				}
			}
		}
		return false
	})
}

func TestCloseReportsARequestedExit(t *testing.T) {
	f := newFakeCLI(t)
	c := f.start(Options{})
	got := make(chan Exited, 1)
	go func() {
		for ev := range c.Events() {
			if e, ok := ev.(Exited); ok {
				got <- e
				return
			}
		}
	}()
	c.Close()
	select {
	case e := <-got:
		if !e.Requested {
			t.Fatal("a close we asked for isn't marked as requested")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no Exited after Close")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 5}
	b.Write([]byte("abc"))
	if got := b.String(); got != "abc" {
		t.Fatalf("got %q", got)
	}
	b.Write([]byte("defgh"))
	if got := b.String(); got != "defgh" {
		t.Fatalf("kept %q, want the last 5 bytes", got)
	}
	if n, err := b.Write([]byte("xyz")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
}
