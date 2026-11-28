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

type mdLink struct {
	label string
	url   string
}

var mdLinkRe = regexp.MustCompile(`\[([^\]\n]+)\]\(((?:https?|file)://[^\s)]+)\)`)

// stripMarkdownLinks turns `[label](url)` outside code spans into plain `label`
// so Glamour does not print the raw URL next to the label, and returns the
// extracted pairs for OSC 8 hyperlinking.
func stripMarkdownLinks(src string) (string, []mdLink) {
	if !strings.Contains(src, "](") {
		return src, nil
	}
	var links []mdLink
	var out strings.Builder
	lines := strings.Split(src, "\n")
	inFence := false
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out.WriteString(line)
			continue
		}
		if inFence || !strings.Contains(line, "](") {
			out.WriteString(line)
			continue
		}
		// Process outside inline backticks.
		parts := strings.Split(line, "`")
		for j, p := range parts {
			if j > 0 {
				out.WriteByte('`')
			}
			if j%2 == 1 || !strings.Contains(p, "](") {
				out.WriteString(p)
				continue
			}
			replaced := mdLinkRe.ReplaceAllStringFunc(p, func(m string) string {
				sub := mdLinkRe.FindStringSubmatch(m)
				if len(sub) != 3 {
					return m
				}
				label, u := sub[1], sub[2]
				if label == "" || label == u {
					return m
				}
				links = append(links, mdLink{label: label, url: u})
				return label
			})
			out.WriteString(replaced)
		}
	}
	return out.String(), links
}

// linkifyLabels wraps extracted Markdown link labels in OSC 8 hyperlinks with
// an underline so they stay clickable without showing raw URLs inline.
func linkifyLabels(out string, links []mdLink) string {
	if len(links) == 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	for _, lk := range links {
		h := fnv.New32a()
		h.Write([]byte(lk.url))
		id := h.Sum32()
		for i := range lines {
			at := strings.Index(lines[i], lk.label)
			if at < 0 {
				continue
			}
			before := lines[i][:at]
			if strings.LastIndex(before, "\x1b]8;id=") > strings.LastIndex(before, "\x1b]8;;\x1b\\") {
				continue
			}
			tagged := fmt.Sprintf("\x1b]8;id=%x;%s\x1b\\\x1b[4m%s\x1b[24m\x1b]8;;\x1b\\", id, lk.url, lk.label)
			lines[i] = lines[i][:at] + tagged + lines[i][at+len(lk.label):]
			break
		}
	}
	return strings.Join(lines, "\n")
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
		if at := strings.Index(lines[i], seg); at >= 0 && seg != "" {
			before := lines[i][:at]
			if strings.LastIndex(before, "\x1b]8;id=") > strings.LastIndex(before, "\x1b]8;;\x1b\\") {
				return
			}
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
