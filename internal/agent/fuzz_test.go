package agent

import "testing"

func FuzzHTMLText(f *testing.F) {
	for _, s := range []string{"<p>a<pre>b</p>", "<code><code>", "<h7>x", "<img alt=''>", "\x00<br>"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { htmlText(s) })
}

func FuzzRules(f *testing.F) {
	f.Add("Bash(go test:*)", "go test && x")
	f.Add("WebFetch(domain:", "::")
	f.Add("(", "")
	f.Fuzz(func(t *testing.T, rule, arg string) {
		in := map[string]any{"command": arg, "url": arg, "file_path": arg}
		for _, tool := range []string{"Bash", "WebFetch", "Read", "mcp__x__y"} {
			ruleMatches(rule, tool, in)
			suggestRule(tool, in)
		}
	})
}
