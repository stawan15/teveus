package ui

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// Settings persist between runs in ~/.config/teveus/settings.json.
type Settings struct {
	Theme      string `json:"theme"`
	HideSide   bool   `json:"hideSidebar"`
	NoMouse    bool   `json:"noMouse"`
	ToolDetail bool   `json:"toolDetail"`

	// Usage savers are on by default; these switch them off.
	LongAnswers bool   `json:"longAnswers"`
	AllTools    bool   `json:"allTools"`
	StylePrompt string `json:"stylePrompt,omitempty"` // replaces the built-in concise prompt

	Engine       string   `json:"engine,omitempty"`       // "claude" (default) or "api"
	Onboarded    bool     `json:"onboarded,omitempty"`    // first-run setup done
	Level        string   `json:"level,omitempty"`        // guidance: "guided", "standard" (default) or "pro"
	ReduceMotion bool     `json:"reduceMotion,omitempty"` // no spinners or shimmer
	APIModel     string   `json:"apiModel,omitempty"`     // last model picked on the API engine
	RecentModels []string `json:"recentModels,omitempty"` // newest first
}

func configDir() string {
	if d := os.Getenv("TEVEUS_CONFIG"); d != "" {
		return d
	}
	var base string
	if runtime.GOOS == "windows" {
		base, _ = os.UserConfigDir()
	} else {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "teveus")
	migrateOnce.Do(func() { migrateOldConfig(filepath.Join(base, "claude-"+"tui"), dir) })
	return dir
}

var migrateOnce sync.Once

// migrateOldConfig moves settings, keys, history and sessions from the app's
// previous name (claude-tui) the first time teveus runs.
func migrateOldConfig(old, dir string) {
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if _, err := os.Stat(old); err == nil {
		os.Rename(old, dir)
	}
}

func LoadSettings() Settings {
	var s Settings
	if b, err := os.ReadFile(filepath.Join(configDir(), "settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(s Settings) {
	os.MkdirAll(configDir(), 0o755)
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(configDir(), "settings.json"), b, 0o644)
}

const historyMax = 500

func loadHistory() []string {
	var h []string
	if b, err := os.ReadFile(filepath.Join(configDir(), "history.json")); err == nil {
		json.Unmarshal(b, &h)
	}
	return h
}

func saveHistory(h []string) {
	if len(h) > historyMax {
		h = h[len(h)-historyMax:]
	}
	os.MkdirAll(configDir(), 0o755)
	b, _ := json.Marshal(h)
	os.WriteFile(filepath.Join(configDir(), "history.json"), b, 0o644)
}

// copyToClipboard tries the platform tool first and falls back to OSC 52,
// which works over SSH in most modern terminals.
func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	if cmd != nil {
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return
		}
	}
	osc52.New(text).WriteTo(os.Stderr)
}

// notify rings the bell and asks the terminal for a desktop notification
// (OSC 9: iTerm2, Ghostty, WezTerm, kitty; ignored elsewhere).
func notify(msg string) {
	os.Stderr.WriteString("\x1b]9;" + msg + "\x07\a")
}

type filesMsg []string

// indexFiles lists project files for @-mentions, preferring git so ignored
// files stay out of the way.
func indexFiles(cwd string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
		cmd.Dir = cwd
		if out, err := cmd.Output(); err == nil {
			return filesMsg(strings.Split(strings.TrimSpace(string(out)), "\n"))
		}
		var files []string
		filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
			if err != nil || len(files) > 20000 {
				return filepath.SkipDir
			}
			name := d.Name()
			if d.IsDir() && p != cwd && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(cwd, p)
				files = append(files, rel)
			}
			return nil
		})
		return filesMsg(files)
	}
}

func gitBranch(cwd string) string {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		b, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
		if err == nil {
			b = bytes.TrimSpace(b)
			if ref, ok := bytes.CutPrefix(b, []byte("ref: refs/heads/")); ok {
				return string(ref)
			}
			if len(b) >= 7 {
				return string(b[:7])
			}
			return ""
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}
