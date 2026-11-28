package antigravity

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

// QuotaGroup holds 5-hour and 7-day rate-limit windows for one model family.
type QuotaGroup struct {
	FiveHour *claude.Window
	SevenDay *claude.Window
}

// UsageSnapshot holds the parsed output of `agy -p /usage --output-format text`.
type UsageSnapshot struct {
	Gemini    QuotaGroup
	ClaudeGPT QuotaGroup
	FetchedAt time.Time
}

// parseAgyUsage parses tab-separated lines emitted by `agy -p /usage --output-format text`:
//
//	Gemini Models\tWeekly Limit Remaining\t48%\t2026-12-03T07:10:29Z
func parseAgyUsage(raw string) (UsageSnapshot, bool) {
	var snap UsageSnapshot
	var found bool
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 3 {
			continue
		}
		groupName := strings.TrimSpace(fields[0])
		windowName := strings.TrimSpace(fields[1])
		pctStr := strings.TrimSuffix(strings.TrimSpace(fields[2]), "%")
		rem, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			continue
		}
		used := 1.0 - rem/100.0
		if used < 0 {
			used = 0
		} else if used > 1 {
			used = 1
		}
		var resetsAt int64
		if len(fields) >= 4 {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[3])); err == nil && t.Year() > 1970 {
				resetsAt = t.Unix()
			}
		}
		win := &claude.Window{Utilization: used, ResetsAt: resetsAt}

		var target *QuotaGroup
		switch {
		case strings.Contains(strings.ToLower(groupName), "gemini"):
			target = &snap.Gemini
		case strings.Contains(strings.ToLower(groupName), "claude"), strings.Contains(strings.ToLower(groupName), "gpt"):
			target = &snap.ClaudeGPT
		default:
			continue
		}

		switch {
		case strings.Contains(strings.ToLower(windowName), "five hour"), strings.Contains(strings.ToLower(windowName), "5"):
			target.FiveHour = win
			found = true
		case strings.Contains(strings.ToLower(windowName), "weekly"):
			target.SevenDay = win
			found = true
		}
	}
	if found {
		snap.FetchedAt = time.Now()
	}
	return snap, found
}

func quotaGroupForModel(snap UsageSnapshot, model string) QuotaGroup {
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "claude-") || strings.HasPrefix(m, "gpt-") {
		if snap.ClaudeGPT.FiveHour != nil || snap.ClaudeGPT.SevenDay != nil {
			return snap.ClaudeGPT
		}
	}
	return snap.Gemini
}

// refreshQuotaAsync queries `agy -p /usage --output-format text` in the
// background and emits claude.RateLimit for the active model family.
func (c *Client) refreshQuotaAsync() {
	if c.closed.Load() || c.bin == "" {
		return
	}
	if !c.quotaBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.quotaBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, c.bin, "-p", "/usage", "--output-format", "text")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		snap, ok := parseAgyUsage(string(out))
		if !ok {
			return
		}
		c.mu.Lock()
		c.quota = &snap
		model := c.model
		c.mu.Unlock()
		c.emitQuotaFor(snap, model)
	}()
}

func (c *Client) emitQuotaFor(snap UsageSnapshot, model string) {
	g := quotaGroupForModel(snap, model)
	if g.FiveHour == nil && g.SevenDay == nil {
		return
	}
	c.emit(claude.RateLimit{
		FiveHour: g.FiveHour,
		SevenDay: g.SevenDay,
	})
}

// RefreshQuota triggers a background quota refresh and returns the latest
// cached snapshot if available.
func (c *Client) RefreshQuota() (*UsageSnapshot, string) {
	c.mu.Lock()
	snap := c.quota
	model := c.model
	c.mu.Unlock()
	c.refreshQuotaAsync()
	return snap, model
}

// FormatUsageTable formats a Markdown table of remaining 5h / weekly quota.
func FormatUsageTable(snap *UsageSnapshot) string {
	if snap == nil {
		return "Fetching Antigravity usage limits in the background — check the USAGE sidebar in a few seconds, or run `/usage` again."
	}
	row := func(name string, g QuotaGroup) string {
		fmtWin := func(w *claude.Window) string {
			if w == nil {
				return "—"
			}
			rem := max(100-int(w.Utilization*100+0.5), 0)
			if w.ResetsAt > 0 {
				return fmt.Sprintf("%d%% left (resets %s)", rem, time.Unix(w.ResetsAt, 0).Local().Format("Mon 15:04"))
			}
			return fmt.Sprintf("%d%% left", rem)
		}
		return fmt.Sprintf("| %s | %s | %s |", name, fmtWin(g.FiveHour), fmtWin(g.SevenDay))
	}
	return strings.Join([]string{
		"| Model Group | 5-Hour Limit | Weekly Limit |",
		"|---|---|---|",
		row("Gemini Models", snap.Gemini),
		row("Claude & GPT", snap.ClaudeGPT),
	}, "\n")
}
