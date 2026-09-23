package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Update check: at most once a day teveus asks GitHub for the latest
// release number, and if it's newer, asks before updating. This is the only
// request teveus makes on its own (see PRIVACY.md); "noUpdateCheck" or
// /settings turns it off. Builds from source ("dev", or versions with a
// "-" or "+") are never checked.

var latestReleaseURL = "https://api.github.com/repos/stawan15/teveus/releases/latest"

const (
	releasesPage     = "https://github.com/stawan15/teveus/releases"
	updateCheckEvery = 24 * time.Hour
)

type updateMsg struct {
	latest string   // e.g. "0.3.2"; "" when up to date
	notes  []string // the release's changelog bullets
	err    error
	asked  bool // from /update: say something even when up to date
}

type updateDoneMsg struct {
	version string
	err     error
}

// releaseVersion reports whether v is a released version (x.y.z).
func releaseVersion(v string) bool {
	_, ok := parseVersion(v)
	return ok && !strings.ContainsAny(v, "-+")
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether version a is newer than b.
func newer(a, b string) bool {
	x, ok1 := parseVersion(a)
	y, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// startupUpdateCheck runs the daily check, if it is due and allowed.
func (m *Model) startupUpdateCheck() tea.Cmd {
	// Not before first-run setup is done: its questions come first.
	if m.settings.NoUpdateCheck || m.cfg.Headless || !releaseVersion(m.cfg.Version) || !m.settings.Onboarded {
		return nil
	}
	if last := time.Unix(m.settings.UpdateCheckedAt, 0); time.Since(last) < updateCheckEvery {
		return nil
	}
	m.settings.UpdateCheckedAt = time.Now().Unix()
	saveSettings(m.settings)
	return checkUpdate(m.cfg.Version, m.settings.SkipVersion, false)
}

// checkUpdate asks GitHub for the latest release. skip is a version the
// user chose to skip.
func checkUpdate(current, skip string, asked bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", latestReleaseURL, nil)
		if err != nil {
			return updateMsg{err: err, asked: asked}
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "teveus/"+current)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return updateMsg{err: err, asked: asked}
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return updateMsg{err: fmt.Errorf("GitHub answered %s", resp.Status), asked: asked}
		}
		var rel struct {
			Tag  string `json:"tag_name"`
			Body string `json:"body"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return updateMsg{err: err, asked: asked}
		}
		latest := strings.TrimPrefix(rel.Tag, "v")
		if !newer(latest, current) || (!asked && latest == skip) {
			return updateMsg{asked: asked}
		}
		return updateMsg{latest: latest, notes: releaseNotes(rel.Body), asked: asked}
	}
}

// releaseNotes keeps the top-level bullets of a release's changelog section.
func releaseNotes(body string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(l, "- "); ok {
			out = append(out, strings.TrimSpace(t))
		}
	}
	return out
}

func (m *Model) handleUpdate(msg updateMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		if msg.asked {
			m.note("couldn't check for updates: "+msg.err.Error(), false)
		}
		return nil // a daily check that fails stays quiet
	case msg.latest == "":
		if msg.asked {
			m.note("✓ teveus "+m.cfg.Version+" is the latest version", true)
		}
		return nil
	}
	// Don't cover a question, an approval or a running task: mention it and
	// let /update bring the question back.
	if m.pop.open() || len(m.perms) > 0 || m.busy {
		m.add(&block{kind: kindInfo, text: fmt.Sprintf("teveus %s is available (you have %s) · /update", msg.latest, m.cfg.Version)})
		m.refresh()
		return nil
	}
	m.askUpdate(msg)
	return nil
}

func (m *Model) askUpdate(msg updateMsg) {
	how, _ := updateCommand(msg.latest)
	body := []string{"", sAccent2.Bold(true).Render("What's new")}
	dots := []lipgloss.Color{cGreen, cBlue, cAccent2, cYellow, cAccent}
	for i, n := range msg.notes {
		if i == 6 {
			body = append(body, sDim.Render(fmt.Sprintf("  … and %d more · %s", len(msg.notes)-6, releasesPage)))
			break
		}
		dot := lipgloss.NewStyle().Foreground(dots[i%len(dots)]).Render("  ◆ ")
		body = append(body, dot+inlineCode(n))
	}
	cs := []choice{
		{label: "Update now", color: cGreen, desc: how, run: func(m *Model) tea.Cmd { return m.runUpdate(msg.latest) }},
		{label: "Later", desc: "ask again tomorrow", run: func(*Model) tea.Cmd { return nil }},
		{label: "Skip " + msg.latest, color: cYellow, desc: "don't ask about this version again", run: func(m *Model) tea.Cmd {
			m.settings.SkipVersion = msg.latest
			saveSettings(m.settings)
			return nil
		}},
		{label: "Stop checking for updates", color: cRed, desc: "turn it back on in /settings", run: func(m *Model) tea.Cmd {
			m.settings.NoUpdateCheck = true
			saveSettings(m.settings)
			m.note("update checks off", true)
			return nil
		}},
	}
	m.openPicker("teveus "+msg.latest+" is available", cs, 0)
	m.pop.body = body
	m.pop.accent = cGreen
	m.pop.tag = pill(m.cfg.Version, cDim) + sDim.Render(" → ") + pill(msg.latest, cGreen)
	m.pop.flat = true
}

// updateCommand is how this copy of teveus updates itself: the way it was
// installed. It returns what to tell the user, and the command (nil when
// updating has to be done by hand).
func updateCommand(version string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "download it from " + releasesPage, nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
	}
	dir := filepath.Dir(exe)
	if isGoBin(dir) {
		return "go install github.com/stawan15/teveus@v" + version,
			[]string{"go", "install", "github.com/stawan15/teveus@v" + version}
	}
	script := fmt.Sprintf("curl -fsSL https://raw.githubusercontent.com/stawan15/teveus/v%s/install.sh | TEVEUS_VERSION=v%s TEVEUS_INSTALL_DIR=%s sh",
		version, version, shellQuote(dir))
	return "downloads the release to " + shortPath(dir) + " and checks its checksum", []string{"sh", "-c", script}
}

// inlineCode renders a changelog line: `code` in colour, the rest as text.
func inlineCode(s string) string {
	parts := strings.Split(s, "`")
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			b.WriteString(sAccent2.Render(p))
		} else {
			b.WriteString(sText.Render(p))
		}
	}
	return b.String()
}

// isGoBin reports whether dir is where go install puts programs.
func isGoBin(dir string) bool {
	if gobin := os.Getenv("GOBIN"); gobin != "" && filepath.Clean(gobin) == dir {
		return true
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	for _, p := range filepath.SplitList(gopath) {
		if filepath.Join(p, "bin") == dir {
			return true
		}
	}
	return false
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// updateNow is /update: check right away, whatever was skipped.
func (m *Model) updateNow() tea.Cmd {
	if !releaseVersion(m.cfg.Version) {
		m.note("this is a build from source ("+m.cfg.Version+"): update it with git pull and go build", false)
		return nil
	}
	m.note("checking for updates…", true)
	return checkUpdate(m.cfg.Version, "", true)
}

func (m *Model) toggleUpdateCheck() tea.Cmd {
	m.settings.NoUpdateCheck = !m.settings.NoUpdateCheck
	saveSettings(m.settings)
	m.note("update checks "+onOff(!m.settings.NoUpdateCheck), true)
	return nil
}

// runUpdate hands the terminal to the installer, then says how it went.
func (m *Model) runUpdate(version string) tea.Cmd {
	how, args := updateCommand(version)
	if args == nil {
		m.note("to update, "+how, false)
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return updateDoneMsg{version: version, err: err} })
}

func (m *Model) handleUpdateDone(msg updateDoneMsg) {
	m.setKittyKeys(true) // the installer had the terminal
	if msg.err != nil {
		_, args := updateCommand(msg.version)
		run := strings.Join(args, " ")
		if len(args) == 3 && args[0] == "sh" {
			run = args[2]
		}
		m.add(&block{kind: kindError, text: "The update didn't finish: " + msg.err.Error() + "\nYou can run it yourself: " + run})
		m.refresh()
		return
	}
	m.add(&block{kind: kindInfo, text: "✓ teveus " + msg.version + " is installed. Quit and start teveus again to use it."})
	m.refresh()
}
