package ui

import "testing"

// Terminal input and pasted text come from outside: parsing must never panic.
func FuzzCSIKey(f *testing.F) {
	for _, s := range []string{"\x1b[13;2u", "\x1b[27;2;13~", "\x1b[99;5u", "\x1b[;u", "\x1b[u", "\x1b[1114112;3u", "\x1b[-5;0u"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) { csiKey(b) })
}

func FuzzSplitPaths(f *testing.F) {
	for _, s := range []string{`a\ b.png 'c d.png'`, `"unterminated`, `\`, `file:///x%zz`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { splitPaths(s) })
}
