package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The rate the renderer runs at is the user's to pick: a terminal that can't
// keep up with 120 FPS shows it as lag while a reply streams.
func TestFrameRateSetting(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	if got := FrameRate(Settings{}); got != 120 {
		t.Fatalf("default rate %d, want 120", got)
	}
	if got := m.frameRate(); got != 120 {
		t.Fatalf("model rate %d", got)
	}

	m.setFrameRate("60")
	if m.settings.FrameRate != 60 || FrameRate(m.settings) != 60 {
		t.Fatalf("setting %d, rate %d", m.settings.FrameRate, FrameRate(m.settings))
	}
	if got := savedFrameRate(t); got != 60 {
		t.Fatalf("saved rate %d", got)
	}

	// "60fps" is the same ask; anything else is refused and says so.
	m.setFrameRate("60fps")
	if m.settings.FrameRate != 60 || !m.noticeOK {
		t.Fatalf("60fps: %d %q", m.settings.FrameRate, m.notice)
	}
	m.setFrameRate("9999")
	if m.settings.FrameRate != 60 || m.noticeOK {
		t.Fatalf("bogus rate accepted: %d %q", m.settings.FrameRate, m.notice)
	}

	// No argument opens the slider, and picking a level sets that rate.
	m.setFrameRate("")
	if m.pop.mode != popSlider || len(m.pop.list) != len(frameRates) {
		t.Fatalf("slider: mode %v, %d levels", m.pop.mode, len(m.pop.list))
	}
	m.pop.list[0].run(m)
	if m.settings.FrameRate != frameRates[0] {
		t.Fatalf("picking %d gave %d", frameRates[0], m.settings.FrameRate)
	}
}

// savedFrameRate reads what setFrameRate wrote to the config dir.
func savedFrameRate(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(configDir(), "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	return s.FrameRate
}
