package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/stawan15/teveus/internal/claude"
)

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.3.2", "0.3.1", true}, {"0.4.0", "0.3.9", true}, {"1.0.0", "0.9.9", true}, {"0.3.1", "0.3.1", false}, {"0.3.0", "0.3.1", false}, {"x", "0.3.1", false}} {
		if newer(c.a, c.b) != c.want {
			t.Errorf("newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
	for v, want := range map[string]bool{"0.3.1": true, "dev": false, "0.3.1+dirty": false, "0.3.2-0.20260923-abc": false} {
		if releaseVersion(v) != want {
			t.Errorf("releaseVersion(%s) != %v", v, want)
		}
	}
}

func fakeReleases(t *testing.T, tag string) (hits *int) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if !strings.HasPrefix(r.UserAgent(), "teveus/") {
			t.Errorf("user agent %q", r.UserAgent())
		}
		fmt.Fprintf(w, `{"tag_name":%q,"body":"### Added\n- Faster startup\n- A new theme\n  - nested detail\n\n### Fixed\n- A crash"}`, tag)
	}))
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })
	return &n
}

// runCmd runs a command and feeds its message back, like Bubble Tea would.
func runCmd(m *Model, c tea.Cmd) {
	if c != nil {
		if msg := c(); msg != nil {
			m.Update(msg)
		}
	}
}

func TestDailyUpdateCheckAsksFirst(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	hits := fakeReleases(t, "v0.3.2")
	m := New(Config{Dark: true, Version: "0.3.1", Settings: Settings{Onboarded: true}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	runCmd(m, m.startupUpdateCheck())
	if *hits != 1 || m.pop.title != "teveus 0.3.2 is available" {
		t.Fatalf("hits=%d popup=%q", *hits, m.pop.title)
	}
	view := strings.Join(strings.Fields(ansi.Strip(m.View())), " ")
	for _, want := range []string{"0.3.1 → 0.3.2", "What's new", "Faster startup", "A new theme", "A crash", "Update now", "Later", "Skip 0.3.2", "Stop checking for updates"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "nested detail") {
		t.Error("nested bullets should stay out of the summary")
	}
	// Once a day: a second start the same day doesn't ask GitHub again.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := New(Config{Dark: true, Version: "0.3.1", Settings: LoadSettings()})
	if c := m2.startupUpdateCheck(); c != nil || *hits != 1 {
		t.Fatal("checked twice in a day")
	}
	// A skipped version isn't offered again, but /update still finds it.
	s := LoadSettings()
	s.UpdateCheckedAt, s.SkipVersion = time.Now().Add(-25*time.Hour).Unix(), "0.3.2"
	m3 := New(Config{Dark: true, Version: "0.3.1", Settings: s})
	runCmd(m3, m3.startupUpdateCheck())
	if m3.pop.open() {
		t.Fatal("skipped version offered again")
	}
	runCmd(m3, m3.updateNow())
	if m3.pop.title != "teveus 0.3.2 is available" {
		t.Fatal("/update didn't offer the skipped version")
	}
}

func TestNoUpdateCheckWhenOffOrFromSource(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	hits := fakeReleases(t, "v9.9.9")
	for name, cfg := range map[string]Config{
		"turned off":     {Version: "0.3.1", Settings: Settings{Onboarded: true, NoUpdateCheck: true}},
		"built from git": {Version: "0.3.1+dirty", Settings: Settings{Onboarded: true}},
		"dev":            {Version: "dev", Settings: Settings{Onboarded: true}},
		"before setup":   {Version: "0.3.1"},
		"teveus -p":      {Version: "0.3.1", Headless: true, Settings: Settings{Onboarded: true}},
	} {
		if c := New(cfg).startupUpdateCheck(); c != nil {
			t.Errorf("%s: checked for updates", name)
		}
	}
	if *hits != 0 {
		t.Fatalf("%d requests", *hits)
	}
}

func TestUpToDateAndBusyStayOutOfTheWay(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	fakeReleases(t, "v0.3.1")
	m := New(Config{Dark: true, Version: "0.3.1", Settings: Settings{Onboarded: true}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	runCmd(m, m.startupUpdateCheck())
	if m.pop.open() || len(m.blocks) != 0 {
		t.Fatal("up to date, but something was shown")
	}
	// A newer version found while busy is mentioned, not asked about.
	fakeReleases(t, "v0.3.2")
	m.busy = true
	m.handleUpdate(updateMsg{latest: "0.3.2"})
	if m.pop.open() || len(m.blocks) != 1 || !strings.Contains(m.blocks[0].text, "/update") {
		t.Fatal("interrupted a running task")
	}
}

func TestUpdateUsesTheWayItWasInstalled(t *testing.T) {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	dir := filepath.Dir(exe)
	t.Setenv("GOBIN", dir)
	if _, args := updateCommand("0.3.2"); strings.Join(args, " ") != "go install github.com/stawan15/teveus@v0.3.2" {
		t.Fatalf("go install copy: %v", args)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", t.TempDir())
	how, args := updateCommand("0.3.2")
	if runtime.GOOS == "windows" {
		if len(args) != 6 || args[0] != "powershell" || !strings.Contains(args[5], "teveus/v0.3.2/install.ps1") ||
			!strings.Contains(args[5], "TEVEUS_VERSION='v0.3.2'") ||
			!strings.Contains(args[5], "TEVEUS_INSTALL_DIR="+psQuote(dir)) || !strings.Contains(how, "checksum") {
			t.Fatalf("installer copy: %q %v", how, args)
		}
		return
	}
	if len(args) != 3 || !strings.Contains(args[2], "teveus/v0.3.2/install.sh") || !strings.Contains(args[2], "TEVEUS_VERSION=v0.3.2") ||
		!strings.Contains(args[2], "TEVEUS_INSTALL_DIR="+shellQuote(dir)) || !strings.Contains(how, "checksum") {
		t.Fatalf("installer copy: %q %v", how, args)
	}
}

func TestSuccessfulUpdateQuits(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The terminal the installer had is taken back, and the program stops
	// rather than carrying on with input it may never read again.
	_, cmd := m.Update(updateDoneMsg{version: "9.9.9"})
	if cmd == nil {
		t.Fatal("a finished update left the app running; ctrl+c may not be read")
	}
	if got := m.Updated(); got != "9.9.9" {
		t.Fatalf("Updated() = %q, want the version just installed", got)
	}
	if m.phone != nil {
		t.Fatal("the phone server is still up after quitting")
	}
}

func TestFailedUpdateKeepsRunning(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	_, cmd := m.Update(updateDoneMsg{version: "9.9.9", err: fmt.Errorf("network down")})
	if cmd != nil {
		t.Fatal("a failed update quit the app; the error can't be read")
	}
	if m.Updated() != "" {
		t.Fatalf("Updated() = %q after a failure", m.Updated())
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "didn't finish") {
		t.Fatalf("the failure isn't shown:\n%s", view)
	}
}

func TestInjectedMessageAppearsWhereItLanded(t *testing.T) {
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.busy = true
	m.add(&block{kind: kindUser, text: "make the tests pass"})
	m.add(&block{kind: kindAssistant, text: "Looking at the failures."})

	// The engine says the user spoke mid-turn: show it there, between steps.
	m.handleEvent(claude.UserMessage{Text: "also update the changelog"})
	m.layout()
	m.refresh()
	view := ansi.Strip(strings.Join(m.lines, "\n"))
	iUser := strings.Index(view, "make the tests pass")
	iNote := strings.Index(view, "also update the changelog")
	if iNote < 0 {
		t.Fatalf("the note never appeared:\n%s", view)
	}
	if iNote < iUser {
		t.Fatalf("the note landed before the turn started:\n%s", view)
	}
}
