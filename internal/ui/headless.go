package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/stawan15/teveus/internal/claude"
)

// HeadlessResult is what "teveus -p" reports, as JSON with -json.
type HeadlessResult struct {
	Result       string   `json:"result"`
	IsError      bool     `json:"is_error"`
	Engine       string   `json:"engine"`
	Model        string   `json:"model"`
	CostUSD      float64  `json:"cost_usd"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	CacheRead    int      `json:"cache_read_tokens"`
	CacheWrite   int      `json:"cache_write_tokens"`
	Turns        int      `json:"num_turns"`
	DurationMS   int64    `json:"duration_ms"`
	Denied       []string `json:"denied_tools,omitempty"`
}

// RunHeadless sends one prompt without the UI, with the same engine and
// savers as the app, and prints the reply (or JSON with asJSON). Tool calls
// that would need approval are denied: pick -mode acceptEdits or auto to let
// them run.
func RunHeadless(cfg Config, prompt string, asJSON bool, stdout, stderr io.Writer) error {
	cfg.Headless = true
	m := New(cfg)
	if m.engine == "claude" && !m.settings.ClaudeNotice {
		fmt.Fprintln(stderr, "note: "+claudeNotice)
	}
	if m.engine == "" {
		return errors.New("nothing is connected: pass -engine claude (your installed Claude Code) or -engine api with -model provider/model")
	}
	m.start(m.cfg.Claude)
	if m.client == nil {
		for _, b := range m.blocks {
			if b.kind == kindError {
				return errors.New(b.text)
			}
		}
		return errors.New("couldn't start the engine")
	}
	defer m.client.Close()
	start := time.Now()
	res := HeadlessResult{Engine: m.engine, Model: m.model}
	if err := m.client.Send(prompt); err != nil {
		return err
	}
	var text strings.Builder
	timeout := time.After(30 * time.Minute)
	for {
		var ev claude.Event
		var ok bool
		select {
		case ev, ok = <-m.client.Events():
		case <-timeout:
			return errors.New("timed out after 30 minutes")
		}
		if !ok {
			return errors.New("the engine stopped before finishing")
		}
		switch e := ev.(type) {
		case claude.Init:
			if e.Model != "" {
				res.Model = e.Model
			}
		case claude.AssistantMessage:
			if e.Parent != "" {
				continue
			}
			for _, b := range e.Blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					text.Reset()
					text.WriteString(b.Text)
				}
			}
		case *claude.PermissionRequest:
			res.Denied = append(res.Denied, e.ToolName)
			fmt.Fprintf(stderr, "denied %s: it needs approval (run with -mode acceptEdits or -mode auto)\n", e.ToolName)
			m.client.Deny(e, "This run is non-interactive and the user didn't allow this tool. Continue without it, or say what you'd need.")
		case claude.Exited:
			if !e.Requested {
				return errors.New("the engine exited unexpectedly")
			}
		case claude.Result:
			res.IsError = e.IsError
			res.CostUSD = e.CostUSD
			res.InputTokens = e.Usage.InputTokens
			res.OutputTokens = e.Usage.OutputTokens
			res.CacheRead = e.Usage.CacheReadInputTokens
			res.CacheWrite = e.Usage.CacheCreationInputTokens
			res.Turns = e.NumTurns
			res.DurationMS = e.DurationMS
			if res.DurationMS == 0 {
				res.DurationMS = time.Since(start).Milliseconds()
			}
			res.Result = text.String()
			if e.IsError && e.Text != "" {
				res.Result = e.Text
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			fmt.Fprintln(stdout, res.Result)
			fmt.Fprintf(stderr, "\n%s · $%.4f · %d in / %d out tokens (%d cached) · %s\n", res.Model, res.CostUSD,
				res.InputTokens, res.OutputTokens, res.CacheRead, fmtDur(time.Duration(res.DurationMS)*time.Millisecond))
			if res.IsError {
				return errors.New("the turn ended with an error")
			}
			return nil
		}
	}
}
