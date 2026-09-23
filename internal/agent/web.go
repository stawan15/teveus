package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// userAgent names teveus honestly, so sites can tell what is fetching.
const userAgent = "teveus/1 (+https://github.com/stawan15/teveus)"

const (
	fetchMaxBytes = 5 << 20
	fetchMaxChars = 40000
)

// runWebFetch downloads a page and returns it as readable text.
func runWebFetch(ctx context.Context, _ *Toolbox, in map[string]any) (string, error) {
	raw := strings.TrimSpace(str(in, "url"))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("not an http(s) URL: %q", raw)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,text/plain,text/markdown,application/json;q=0.9,*/*;q=0.5")
	resp, err := httpDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s returned %s", u.Host, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes))
	if err != nil {
		return "", err
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var text string
	switch {
	case mt == "text/html" || mt == "application/xhtml+xml" || (mt == "" && strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<html")):
		text = htmlText(string(body))
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"), mt == "application/xml", mt == "":
		text = string(body)
	default:
		return "", fmt.Errorf("%s is %s, not a page teveus can read", u, mt)
	}
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > fetchMaxChars {
		text = string(r[:fetchMaxChars]) + fmt.Sprintf("\n… (%d more characters not shown)", len(r)-fetchMaxChars)
	}
	final := resp.Request.URL.String()
	if final != u.String() {
		return "Redirected to " + final + "\n\n" + text, nil
	}
	return text, nil
}

// htmlText renders HTML as plain Markdown-ish text: headings, list items,
// links and code blocks survive; scripts, styles and navigation chrome go.
func htmlText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	var sb strings.Builder
	var title string
	pre := 0
	var walk func(*html.Node)
	block := func() {
		if t := sb.String(); t != "" && !strings.HasSuffix(t, "\n\n") {
			if strings.HasSuffix(t, "\n") {
				sb.WriteString("\n")
			} else {
				sb.WriteString("\n\n")
			}
		}
	}
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			if pre > 0 {
				sb.WriteString(n.Data)
				return
			}
			t := strings.Join(strings.Fields(n.Data), " ")
			if t == "" {
				if strings.TrimSpace(n.Data) == "" && n.Data != "" && !strings.HasSuffix(sb.String(), " ") && !strings.HasSuffix(sb.String(), "\n") {
					sb.WriteString(" ")
				}
				return
			}
			if n.Data[0] == ' ' || n.Data[0] == '\n' || n.Data[0] == '\t' {
				if s := sb.String(); s != "" && !strings.HasSuffix(s, " ") && !strings.HasSuffix(s, "\n") {
					sb.WriteString(" ")
				}
			}
			sb.WriteString(t)
			if last := n.Data[len(n.Data)-1]; last == ' ' || last == '\n' || last == '\t' {
				sb.WriteString(" ")
			}
			return
		}
		if n.Type != html.ElementNode && n.Type != html.DocumentNode {
			return
		}
		tag := n.Data
		switch tag {
		case "script", "style", "noscript", "svg", "nav", "footer", "iframe", "form", "button", "template":
			return
		case "title":
			if n.FirstChild != nil {
				title = strings.TrimSpace(n.FirstChild.Data)
			}
			return
		case "br":
			sb.WriteString("\n")
			return
		case "hr":
			block()
			sb.WriteString("---")
			block()
			return
		case "img":
			for _, a := range n.Attr {
				if a.Key == "alt" && strings.TrimSpace(a.Val) != "" {
					sb.WriteString("[image: " + strings.TrimSpace(a.Val) + "]")
				}
			}
			return
		}
		switch tag {
		case "p", "div", "section", "article", "main", "header", "table", "tr", "blockquote", "ul", "ol", "dl", "figure":
			block()
		case "h1", "h2", "h3", "h4", "h5", "h6":
			block()
			sb.WriteString(strings.Repeat("#", int(tag[1]-'0')) + " ")
		case "li":
			if s := sb.String(); s != "" && !strings.HasSuffix(s, "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("- ")
		case "pre":
			block()
			sb.WriteString("```\n")
			pre++
		case "code":
			if pre == 0 {
				sb.WriteString("`")
			}
		case "td", "th":
			sb.WriteString(" | ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		switch tag {
		case "pre":
			pre--
			if !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("```")
			block()
		case "code":
			if pre == 0 {
				sb.WriteString("`")
			}
		case "a":
			for _, a := range n.Attr {
				if a.Key == "href" && strings.HasPrefix(a.Val, "http") {
					sb.WriteString(" (" + a.Val + ")")
				}
			}
		case "p", "div", "section", "article", "main", "header", "table", "tr", "blockquote", "ul", "ol", "dl", "figure",
			"h1", "h2", "h3", "h4", "h5", "h6":
			block()
		}
	}
	walk(doc)
	out := sb.String()
	// Collapse runs of blank lines left by empty containers.
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	out = strings.TrimSpace(strings.Join(lines, "\n"))
	if title != "" {
		out = "# " + title + "\n\n" + out
	}
	return out
}

// SearchProviders are web search APIs, used with the user's own key. They
// are kept apart from model providers: they have no models to list.
var SearchProviders = []Provider{
	{ID: "brave", Name: "Brave Search", EnvKeys: []string{"BRAVE_API_KEY", "BRAVE_SEARCH_API_KEY"},
		KeyURL: "https://api-dashboard.search.brave.com/app/keys", BaseURL: "https://api.search.brave.com/res/v1/web/search"},
	{ID: "tavily", Name: "Tavily", EnvKeys: []string{"TAVILY_API_KEY"},
		KeyURL: "https://app.tavily.com/home", BaseURL: "https://api.tavily.com/search"},
}

// IsSearch reports whether p is a web search provider.
func (p Provider) IsSearch() bool {
	for _, s := range SearchProviders {
		if s.ID == p.ID {
			return true
		}
	}
	return false
}

type searchHit struct{ title, url, snippet string }

// searchKey returns the first connected search provider.
func searchKey(s *Store) (Provider, Credential, bool) {
	if s == nil {
		return Provider{}, Credential{}, false
	}
	for _, p := range SearchProviders {
		if c, src, _ := s.Lookup(p); src != FromNone && c.Key != "" {
			return p, c, true
		}
	}
	return Provider{}, Credential{}, false
}

func search(ctx context.Context, p Provider, key, query string, n int) ([]searchHit, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var req *http.Request
	var err error
	switch p.ID {
	case "brave":
		q := url.Values{"q": {query}, "count": {fmt.Sprint(n)}}
		req, err = http.NewRequestWithContext(ctx, "GET", p.BaseURL+"?"+q.Encode(), nil)
		if err == nil {
			req.Header.Set("X-Subscription-Token", key)
			req.Header.Set("Accept", "application/json")
		}
	case "tavily":
		b, _ := json.Marshal(map[string]any{"query": query, "max_results": n})
		req, err = http.NewRequestWithContext(ctx, "POST", p.BaseURL, strings.NewReader(string(b)))
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		return nil, fmt.Errorf("unknown search provider %q", p.ID)
	}
	if err != nil {
		return nil, err
	}
	resp, err := httpDo(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, apiError(resp)
	}
	var hits []searchHit
	switch p.ID {
	case "brave":
		var out struct {
			Web struct {
				Results []struct {
					Title       string `json:"title"`
					URL         string `json:"url"`
					Description string `json:"description"`
				} `json:"results"`
			} `json:"web"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
		for _, r := range out.Web.Results {
			hits = append(hits, searchHit{r.Title, r.URL, htmlText(r.Description)})
		}
	case "tavily":
		var out struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
		for _, r := range out.Results {
			hits = append(hits, searchHit{r.Title, r.URL, r.Content})
		}
	}
	return hits, nil
}

func (e *Engine) runWebSearch(ctx context.Context, in map[string]any) (string, error) {
	p, cred, ok := searchKey(e.opts.Store)
	if !ok {
		return "", errors.New("web search isn't set up: connect Brave Search or Tavily with /login")
	}
	query := strings.TrimSpace(str(in, "query"))
	if query == "" {
		return "", errors.New("query is empty")
	}
	hits, err := search(ctx, p, cred.Key, query, 8)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "No results.", nil
	}
	var sb strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, h.title, h.url)
		if s := strings.TrimSpace(h.snippet); s != "" {
			if r := []rune(s); len(r) > 400 {
				s = string(r[:400]) + "…"
			}
			sb.WriteString("   " + s + "\n")
		}
	}
	return sb.String(), nil
}

// VerifySearch checks a search key with a tiny query.
func VerifySearch(ctx context.Context, p Provider, c Credential) error {
	_, err := search(ctx, p, c.Key, "teveus", 1)
	return err
}
