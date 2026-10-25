package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

// openCodeUsageURL is the Go plan's usage endpoint. The Console plan (a base
// without /go) has none, so nothing is asked of it.
func openCodeUsageURL(base string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/go") {
		return ""
	}
	return base + "/v1/usage"
}

// openCodeUsage reads how much of each allowance window the Go plan has used.
// A nil result means this plan doesn't report usage.
func openCodeUsage(ctx context.Context, base, key string) (*claude.RateLimit, error) {
	url := openCodeUsageURL(base)
	if url == "" {
		return nil, nil
	}
	resp, err := send(ctx, "GET", url, nil, func(h http.Header) {
		if key != "" {
			h.Set("Authorization", "Bearer "+key)
		}
		h.Set("User-Agent", userAgent)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Usage map[string]struct {
			Status   string  `json:"status"`
			Percent  float64 `json:"percent"`
			ResetsAt string  `json:"resetsAt"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	win := func(name string) *claude.Window {
		w, ok := out.Usage[name]
		if !ok {
			return nil
		}
		res := &claude.Window{Utilization: w.Percent / 100}
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			res.ResetsAt = t.Unix()
		}
		return res
	}
	rl := claude.RateLimit{
		Status:   out.Usage["rolling"].Status,
		FiveHour: win("rolling"),
		SevenDay: win("weekly"),
		Monthly:  win("monthly"),
	}
	if rl.FiveHour == nil && rl.SevenDay == nil && rl.Monthly == nil {
		return nil, nil
	}
	return &rl, nil
}
