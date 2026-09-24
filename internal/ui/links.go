package ui

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Terminals make text clickable (cmd or ctrl + click) when it is wrapped in
// an OSC 8 hyperlink. Plain-text URL detection fails once a URL is styled,
// broken over two lines, or the terminal has mouse reporting on, so the
// transcript marks its own.

var urlRe = regexp.MustCompile(`https?://[^\s\x1b<>"'` + "`" + `]+`)

// trimURL drops the punctuation a sentence puts after a link.
func trimURL(u string) string {
	u = strings.TrimRight(u, ".,;:!?*")
	if strings.HasSuffix(u, ")") && !strings.Contains(u, "(") {
		u = strings.TrimSuffix(u, ")")
	}
	return u
}

// linkify marks the URLs of src in the rendered lines out. The wrapping
// breaks a long URL over lines (and pads them), so each URL is looked for
// as text across lines, and every piece points at the whole address.
func linkify(out, src string) string {
	if !strings.Contains(src, "http") {
		return out
	}
	var urls []string
	for _, u := range urlRe.FindAllString(src, -1) {
		if u = trimURL(u); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = strings.TrimSpace(ansi.Strip(l))
	}
	mark := func(i int, seg, u string) {
		if at := strings.Index(lines[i], seg); at >= 0 && seg != "" && !strings.Contains(lines[i][:at], "\x1b]8;") {
			h := fnv.New32a()
			h.Write([]byte(u))
			lines[i] = lines[i][:at] + fmt.Sprintf("\x1b]8;id=%x;%s\x1b\\%s\x1b]8;;\x1b\\", h.Sum32(), u, seg) + lines[i][at+len(seg):]
		}
	}
	for _, u := range urls {
		for i := range lines {
			if strings.Contains(plain[i], u) {
				mark(i, u, u)
				continue
			}
			// The address starts at the end of this line and goes on below.
			for k := min(len(u)-1, len(plain[i])); k >= len("http://x"); k-- {
				if !strings.HasSuffix(plain[i], u[:k]) {
					continue
				}
				type piece struct {
					line int
					seg  string
				}
				pieces := []piece{{i, u[:k]}}
				for pos, j := k, i+1; pos < len(u); j++ {
					rest := u[pos:]
					switch {
					case j >= len(lines) || plain[j] == "":
						pieces = nil
					case strings.HasPrefix(rest, plain[j]):
						pieces = append(pieces, piece{j, plain[j]})
						pos += len(plain[j])
						continue
					case strings.HasPrefix(plain[j], rest):
						pieces = append(pieces, piece{j, rest})
					default:
						pieces = nil
					}
					break
				}
				for _, p := range pieces {
					mark(p.line, p.seg, u)
				}
				if pieces != nil {
					break
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}
