package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes a stand-in for the claude binary: a shell script that records
// what it was given and answers each request the way the real CLI does. The
// JSON lines it prints come from script, and everything it reads is appended to
// a log so a test can check the protocol.
type fakeCLI struct {
	t    *testing.T
	dir  string
	path string
	log  string // where the child appends what it was told

}

// newFakeCLI creates the script. replies are the JSON lines to print once, in
// order, before the loop that answers control requests forever.
func newFakeCLI(t *testing.T, replies ...string) *fakeCLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a POSIX shell script")
	}
	dir := t.TempDir()
	f := &fakeCLI{t: t, dir: dir, path: filepath.Join(dir, "fake-claude"),
		log: filepath.Join(dir, "seen.log")}
	// - After the canned replies, answer control requests: an init gets a
	//   result, a permission prompt is echoed as a can_use_tool request.
	// - Anything read is logged so tests can assert on the protocol.
	body := `#!/bin/sh
# Arguments go in one write, ending with a marker, so a reader never sees a
# partial list. "started" is written last: once it exists, args.log is whole.
{
  for a in "$@"; do printf '%s\n' "$a"; done
  printf '%s\n' "--done--"
} > "` + dir + `/args.log"
: > "` + dir + `/started.log"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + f.log + `"
  case "$line" in
` + f.replyBlock(replies) + `
    *'"initialize"'*) printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"init-1","response":{"commands":[],"models":[]}}}' ;;
    *'"can_use_tool"'*) : ;;
  esac
done
`
	if err := os.WriteFile(f.path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// replyBlock prints the canned lines once, on first input, then never again.
func (f *fakeCLI) replyBlock(replies []string) string {
	if len(replies) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("    first)\n")
	sb.WriteString("      first=0\n")
	for _, r := range replies {
		sb.WriteString("      printf '%s\\n' '" + shellQuote(r) + "'\n")
	}
	sb.WriteString("      ;;\n")
	return sb.String()
}

// shellQuote makes a string safe inside single quotes.
func shellQuote(s string) string { return strings.ReplaceAll(s, "'", `'\''`) }

// start runs the client against the script.
func (f *fakeCLI) start(opts Options) *Client {
	f.t.Helper()
	opts.Binary = f.path
	if opts.Cwd == "" {
		opts.Cwd = f.dir
	}
	// The script prints its canned replies on the first line it reads; the
	// client's own initialize does that, so a test sees them immediately.
	if opts.ExtraArgs == nil {
		opts.ExtraArgs = []string{"first"}
	}
	c, err := Start(opts)
	if err != nil {
		f.t.Fatalf("start: %v", err)
	}
	f.t.Cleanup(c.Close)
	return c
}

// args is what the script was invoked with.
func (f *fakeCLI) args() []string {
	f.t.Helper()
	// The script writes its arguments one printf at a time and ends the list
	// with --done--, so waiting for that marker, not just for a non-empty
	// file, is what keeps a test from reading half of them.
	waitFor(f.t, func() bool {
		b, err := os.ReadFile(filepath.Join(f.dir, "args.log"))
		return err == nil && strings.Contains(string(b), "--done--\n")
	})
	b, err := os.ReadFile(filepath.Join(f.dir, "args.log"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "--done--" {
			break
		}
		out = append(out, line)
	}
	return out
}

// told returns the JSON lines the client sent the script, decoded.
func (f *fakeCLI) told() []map[string]any {
	f.t.Helper()
	fh, err := os.Open(f.log)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []map[string]any
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// waitFor polls until cond is true or the test times out.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting")
}
