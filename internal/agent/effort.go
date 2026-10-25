package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stawan15/teveus/internal/claude"
)

// Reasoning effort is one scale in the app and a different set of words at
// every provider, and some models don't take the setting at all. This file is
// the single place that knows the difference: the engine clamps the level the
// user picked to what the model can actually do, so the clients just send what
// they are given.
//
//	"" (auto) < low < medium < high < xhigh < max
var effortScale = []string{"", "low", "medium", "high", "xhigh", "max"}

func effortRank(level string) int {
	for i, l := range effortScale {
		if l == level {
			return i
		}
	}
	return 0 // anything unknown means "leave it to the model"
}

// protocolEffort is the highest level a wire protocol can express. OpenAI-style
// APIs take low/medium/high; Claude Code and Anthropic also take max, and the
// CLI its own xhigh.
func protocolEffort(protocol Protocol) int {
	switch protocol {
	case Anthropic:
		return effortRank("max")
	}
	return effortRank("high")
}

// clampEffort lowers a level to the highest one the protocol understands,
// reporting whether it had to. Lowering is never silent: the caller says so.
func clampEffort(level string, protocol Protocol) (string, bool) {
	want := effortRank(level)
	if max := protocolEffort(protocol); want > max {
		return effortScale[max], true
	}
	return level, false
}

// ClampEffort is the ceiling for a "provider/model" pick, for the UI to show
// the level the model is really asked for.
func ClampEffort(level, model string) (string, bool) {
	p := OpenAI
	if pid, _, ok := strings.Cut(model, "/"); ok {
		if known, found := ProviderByID(pid); found {
			p = known.Protocol
		}
	}
	return clampEffort(level, p)
}

// effortCaps remembers, per model, the highest effort a provider accepted.
// It is learned from a refusal rather than guessed from a table, so it can't
// go stale when a model changes, and it costs one retry ever.
type effortCaps struct {
	path string
	mu   sync.Mutex
	m    map[string]string // "provider/model" -> highest level that worked ("" = none)
}

func loadEffortCaps(dir string) *effortCaps {
	c := &effortCaps{m: map[string]string{}}
	if dir == "" {
		return c // nowhere to keep it: remember for this process only
	}
	c.path = filepath.Join(dir, "effort.json")
	if b, err := os.ReadFile(c.path); err == nil {
		json.Unmarshal(b, &c.m)
	}
	return c
}

// capFor is the learned ceiling for a model, or ok=false when nothing is known.
func (c *effortCaps) capFor(model string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[model]
	return v, ok
}

// learn records what a refusal means for a model. Two kinds happen: the model
// doesn't take the effort field at all (ask for nothing next time), or it takes
// the field but not this value (ask for one below). The error says which.
func (c *effortCaps) learn(model, level string, err error) {
	if c == nil || model == "" {
		return
	}
	below := ""
	if mentionsLevel(err, level) {
		if r := effortRank(level); r > 0 {
			below = effortScale[r-1]
		}
	}
	c.mu.Lock()
	if old, ok := c.m[model]; ok && effortRank(old) <= effortRank(below) {
		c.mu.Unlock()
		return // already known, and this refusal isn't news
	}
	c.m[model] = below
	b, _ := json.Marshal(c.m)
	path := c.path
	c.mu.Unlock()
	if path == "" {
		return // nothing saved, but this process knows
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, b, 0o600)
}

// mentionsLevel reports whether an error names the effort value it rejected,
// which means the field is fine and only that value is not.
func mentionsLevel(err error, level string) bool {
	if err == nil || level == "" {
		return false
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, strings.ToLower(level)) {
		return false
	}
	// "does not support effort" mentions the field, not the value.
	for _, field := range []string{"does not support effort", "not support reasoning", "unknown parameter", "unrecognized request argument"} {
		if strings.Contains(s, field) {
			return false
		}
	}
	return true
}

// effortFor is the level to actually send for a model: the user's pick,
// lowered to the protocol's ceiling and to anything learned from refusals.
// modelID is the bare model; the provider is read from the full pick so the
// right protocol applies (Anthropic takes more levels than OpenAI-style APIs).
func (e *Engine) effortFor(modelID string) (level string, why string) {
	e.mu.Lock()
	want, full := e.opts.Effort, e.model
	e.mu.Unlock()
	if want == "" {
		return "", ""
	}
	protocol := OpenAI
	if pid, _, ok := strings.Cut(full, "/"); ok {
		if p, known := ProviderByID(pid); known {
			protocol = p.Protocol
		}
	}
	level, capped := clampEffort(want, protocol)
	if capped {
		why = "this API stops at " + level
	}
	if known, ok := e.effortCap.capFor(modelID); ok && effortRank(level) > effortRank(known) {
		level = known
		if known == "" {
			why = "this model doesn't take the effort setting"
		} else {
			why = "this model refused a higher setting"
		}
	}
	return level, why
}

// noteEffortRejected records that a model refused the requested level, so the
// next request asks for less instead of spending a retry every time.
func (e *Engine) noteEffortRejected(modelID, level string, err error) {
	if level == "" {
		return
	}
	e.effortCap.learn(modelID, level, err)
}

// sayEffortOnce tells the user, in the conversation, that the level they picked
// was lowered for this model. Said once per engine so a long session isn't
// spammed, and never when nothing was lowered.
func (e *Engine) sayEffortOnce(why string) {
	if why == "" {
		return
	}
	e.mu.Lock()
	said := e.effortDropped
	e.effortDropped = true
	e.mu.Unlock()
	if said {
		return
	}
	e.emit(claude.Notice{Text: "Effort lowered for this model: " + why})
}
