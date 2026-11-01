package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UsageRecord is one billed model response, appended to usage.jsonl.
//
// The file is a plain append-only ledger so other tools (the claude-monitor
// menu bar app, scripts) can read real token usage and cost without talking to
// a running teveus. Field names are lowerCamelCase to match those readers.
type UsageRecord struct {
	Time       time.Time `json:"time"`
	ID         string    `json:"id"`
	Session    string    `json:"session"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Project    string    `json:"project"`
	Cwd        string    `json:"cwd"`
	Input      int       `json:"input"`
	Output     int       `json:"output"`
	CacheRead  int       `json:"cacheRead"`
	CacheWrite int       `json:"cacheWrite"`
	Cost       float64   `json:"cost"`
	Subagent   bool      `json:"subagent"`
}

// UsagePath is the ledger location inside teveus's config directory.
func UsagePath(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "usage.jsonl")
}

var usageMu sync.Mutex

// recordUsage appends one response to the ledger. Failures are ignored: the
// ledger is for reporting, and a full disk must not fail the conversation.
func (e *Engine) recordUsage(modelID string, u Usage, subagent bool) {
	path := UsagePath(e.opts.ConfigDir)
	if path == "" || (u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0) {
		return
	}
	e.mu.Lock()
	session, model := e.session, e.model
	e.mu.Unlock()

	provider, _, _ := strings.Cut(model, "/")
	now := time.Now().UTC()
	rec := UsageRecord{
		Time:       now,
		ID:         now.Format("20060102150405.000000000") + "-" + session,
		Session:    session,
		Provider:   provider,
		Model:      modelID,
		Project:    filepath.Base(e.opts.Cwd),
		Cwd:        e.opts.Cwd,
		Input:      u.Input,
		Output:     u.Output,
		CacheRead:  u.CacheRead,
		CacheWrite: u.CacheWrite,
		Cost:       u.Cost,
		Subagent:   subagent,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')

	usageMu.Lock()
	defer usageMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(line)
	_ = f.Close()
}
