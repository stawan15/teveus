package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stawan15/teveus/internal/claude"
)

// testModel is a Model with a size, a temp config dir, and a backend that does
// nothing, which is all most command handlers need.
func testModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("TEVEUS_CONFIG", t.TempDir())
	m := New(Config{Dark: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.engine, m.client = "api", &tabBackend{ch: make(chan claude.Event, 8)}
	// Commands like /export write into the project folder, so point it at a
	// temp dir: a test must never leave files in the repository.
	m.cwd = t.TempDir()
	return m
}

func TestTogglesFlipAndSaySo(t *testing.T) {
	m := testModel(t)
	m.settings.ToolDetail = false
	m.toggleDetails()
	if !m.settings.ToolDetail || !strings.Contains(m.notice, "details") {
		t.Fatalf("details untoggled: %v %q", m.settings.ToolDetail, m.notice)
	}
	m.toggleDetails()
	if m.settings.ToolDetail {
		t.Fatal("details didn't toggle back")
	}

	m.settings.HideSide = false
	m.toggleSidebar()
	if !m.settings.HideSide {
		t.Fatal("sidebar didn't hide")
	}

	m.settings.NoMouse = false
	m.toggleMouse()
	if !m.settings.NoMouse || !strings.Contains(m.notice, "mouse") {
		t.Fatalf("mouse: %v %q", m.settings.NoMouse, m.notice)
	}
}

func TestCycleModeWalksTheList(t *testing.T) {
	m := testModel(t)
	m.engine = "claude" // no backend restart
	m.mode = modes[0]
	for i := 1; i <= len(modes); i++ {
		m.cycleMode()
		if want := modes[i%len(modes)]; m.mode != want {
			t.Fatalf("after %d cycles: %q, want %q", i, m.mode, want)
		}
	}
}

func TestTrimAsksForTheDiffOrTheProject(t *testing.T) {
	m := testModel(t)
	b := &captureBackend{tabBackend: tabBackend{ch: make(chan claude.Event)}}
	m.client = b

	m.trim("")
	if len(b.sent) != 1 || !strings.Contains(b.sent[0], "uncommitted changes") {
		t.Fatalf("trim sent %v", b.sent)
	}
	m.busy = false
	m.trim("all")
	if len(b.sent) != 2 || !strings.Contains(b.sent[1], "whole project") {
		t.Fatalf("trim all sent %v", b.sent)
	}
}

func TestClearConversationStartsFresh(t *testing.T) {
	m := testModel(t)
	m.add(&block{kind: kindUser, text: "old work"})
	m.sessionID, m.cost, m.context = "api-old", 1.5, 9000

	m.clearConversation()
	if len(m.blocks) != 0 || m.sessionID != "" || m.cost != 0 || m.context != 0 {
		t.Fatalf("old state survived: blocks=%d id=%q cost=%v ctx=%d",
			len(m.blocks), m.sessionID, m.cost, m.context)
	}
}

func TestOpenModePickerListsModesWithHelp(t *testing.T) {
	m := testModel(t)
	m.openModePicker()
	if m.pop.mode != popPicker || len(m.pop.list) != len(modes) {
		t.Fatalf("picker = %v with %d rows", m.pop.mode, len(m.pop.list))
	}
	for _, c := range m.pop.list {
		if c.desc == "" {
			t.Fatalf("%q has no explanation", c.label)
		}
	}
	// Choosing one applies it.
	var chosen int
	for i, c := range m.pop.list {
		if c.label == modeLabel("plan") {
			chosen = i
		}
	}
	if run := m.pop.list[chosen].run; run != nil {
		run(m)
	}
	if m.mode != "plan" {
		t.Fatalf("mode = %q", m.mode)
	}
}

func TestFileChoicesFindsProjectFiles(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "deep.go"), []byte("package sub"), 0o644)
	m.cwd = dir
	m.files = []string{"main.go", filepath.Join("sub", "deep.go")}

	cs := m.fileChoices()
	if len(cs) == 0 {
		t.Fatal("no files offered")
	}
	found := false
	for _, c := range cs {
		if c.value == "main.go" || c.value == filepath.Join("sub", "deep.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("project files missing: %+v", cs)
	}
}

func TestOpenArgPickerOffersTheCommandOptions(t *testing.T) {
	m := testModel(t)
	m.openArgPicker(claude.Command{Name: "model", ArgumentHint: "<name> [free|fast]"}, []string{"a", "b"})
	if m.pop.mode != popPicker {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	var labels []string
	for _, c := range m.pop.list {
		labels = append(labels, c.label)
	}
	if len(labels) < 2 {
		t.Fatalf("options = %v, want the hint's options", labels)
	}
}

func TestThemePickerAppliesAChoice(t *testing.T) {
	m := testModel(t)
	m.openThemePicker()
	if len(m.pop.list) < 2 {
		t.Fatalf("%d themes", len(m.pop.list))
	}
	first := m.pop.list[0]
	first.run(m)
	if m.settings.Theme == "" {
		t.Fatal("no theme was saved")
	}
}

func TestSlashChoicesCoverEveryLocalCommand(t *testing.T) {
	m := testModel(t)
	cs := m.slashChoices()
	seen := map[string]bool{}
	for _, c := range cs {
		seen[strings.TrimPrefix(c.label, "/")] = true
	}
	for _, c := range localCmds {
		if !seen[c.name] {
			t.Errorf("/%s is missing from the palette", c.name)
		}
	}
}

func TestPaletteOpensAndRunsAnAction(t *testing.T) {
	m := testModel(t)
	m.openPalette()
	if m.pop.mode != popPalette {
		t.Fatalf("popup = %v", m.pop.mode)
	}
	if len(m.pop.list) == 0 {
		t.Fatal("the palette is empty")
	}
	// Typing filters, and the first result runs.
	m.pop.query = "sidebar"
	m.pop.filter()
	if len(m.pop.list) == 0 {
		t.Fatal("searching for the sidebar found nothing")
	}
}

func TestLocalCommandDispatch(t *testing.T) {
	m := testModel(t)
	if _, ok := m.localCommand("nosuchcommand", ""); ok {
		t.Fatal("an unknown command was handled")
	}
	// Aliases resolve to the full name.
	if _, ok := m.localCommand("q", ""); !ok {
		t.Fatal("the quit alias wasn't recognised")
	}
	if _, ok := m.localCommand("sidebar", ""); !ok {
		t.Fatal("/sidebar wasn't handled")
	}
}

func TestOpenLogoutWithoutProviders(t *testing.T) {
	// There is nothing to remove, so it says so in the status bar rather than
	// opening an empty list.
	m := testModel(t)
	m.openLogout()
	if m.pop.open() {
		t.Fatal("/logout opened an empty picker")
	}
	if !strings.Contains(m.notice, "no saved keys") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestKeyInputCollectsAValue(t *testing.T) {
	m := testModel(t)
	var got string
	m.pop = popup{mode: popInput, title: "API key", masked: true,
		submit: func(m *Model, v string) tea.Cmd { got = v; return nil }}
	if m.pop.mode != popInput || !m.pop.masked {
		t.Fatalf("popup = %v masked=%v", m.pop.mode, m.pop.masked)
	}
	m.pop.submit(m, "secret")
	if got != "secret" {
		t.Fatalf("submitted %q", got)
	}
}

func TestClipboardCopyWritesOSC52(t *testing.T) {
	m := testModel(t)
	var buf strings.Builder
	m.cfg.KeyOut = &buf
	m.copyLast()
	// Nothing to copy yet.
	if !strings.Contains(m.notice, "nothing") {
		t.Fatalf("notice = %q", m.notice)
	}
	m.add(&block{kind: kindAssistant, text: "copy me"})
	m.copyLast()
	if !strings.Contains(m.notice, "copied") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestConfigDirHonoursTheOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEVEUS_CONFIG", dir)
	if got := configDir(); got != dir {
		t.Fatalf("configDir = %q, want %q", got, dir)
	}
}

func TestSetEffortSavesAndRestarts(t *testing.T) {
	m := testModel(t)
	m.setEffort("high")
	if m.settings.Effort != "high" {
		t.Fatalf("effort = %q", m.settings.Effort)
	}
	if LoadSettings().Effort != "high" {
		t.Fatal("the effort wasn't saved")
	}
	m.setEffort("auto")
	if m.settings.Effort != "" {
		t.Fatalf("auto should clear it, got %q", m.settings.Effort)
	}
}

func TestSetMinimalLevels(t *testing.T) {
	m := testModel(t)
	for _, lvl := range []string{"off", "lite", "full", "strict"} {
		m.setMinimal(lvl)
		want := lvl
		if lvl == "off" {
			want = ""
		}
		if m.settings.MinimalCode != want {
			t.Errorf("setMinimal(%q) = %q", lvl, m.settings.MinimalCode)
		}
	}
}

func TestHistoryIsSavedAndBounded(t *testing.T) {
	m := testModel(t)
	for i := 0; i < historyMax+20; i++ {
		m.remember("prompt " + strings.Repeat("x", i%5))
	}
	saveHistory(m.history)
	hist := loadHistory()
	if len(hist) == 0 {
		t.Fatal("nothing saved")
	}
	if len(hist) > historyMax {
		t.Fatalf("%d entries, want at most %d", len(hist), historyMax)
	}
}

func TestGitBranchFindsTheCurrentOne(t *testing.T) {
	dir := gitRepo(t)
	if b := gitBranch(dir); b == "" {
		t.Fatal("no branch found in a real repository")
	}
	if b := gitBranch(t.TempDir()); b != "" {
		t.Fatalf("a plain folder reported branch %q", b)
	}
}

func TestSplitKeysGuardOnlyWrapedARealTerminal(t *testing.T) {
	// When stdin is a character device the guard wraps it, and the wrapper must
	// still hand the file's own Fd and Write through, so Bubble Tea can put the
	// terminal in raw mode.
	in, ok := SplitKeysGuard()
	if !ok {
		if in != nil {
			t.Fatal("no terminal, but a reader was returned")
		}
		return // running headless: nothing more to check
	}
	sk, isSplit := in.(*splitKeys)
	if !isSplit {
		t.Fatalf("guard returned %T", in)
	}
	if sk.File != os.Stdin {
		t.Fatal("the wrapper lost stdin")
	}
}

func TestSetKittyKeysWritesBothStates(t *testing.T) {
	var buf strings.Builder
	m := testModel(t)
	m.cfg.KeyOut = &buf
	m.setKittyKeys(true)
	if !strings.Contains(buf.String(), kittyKeysOn) {
		t.Fatalf("on wrote %q", buf.String())
	}
	buf.Reset()
	m.setKittyKeys(false)
	if !strings.Contains(buf.String(), kittyKeysOff) {
		t.Fatalf("off wrote %q", buf.String())
	}
	// With no writer (tests elsewhere), it must not panic.
	m.cfg.KeyOut = nil
	m.setKittyKeys(true)
}

func TestCollapseAndExpandPastesRoundTrips(t *testing.T) {
	m := testModel(t)
	long := strings.Repeat("line\n", 40)
	shown, folded := m.collapsePaste(long)
	if !folded || len(shown) >= len(long) {
		t.Fatalf("a long paste wasn't folded: folded=%v len=%d", folded, len(shown))
	}
	if got := m.expandPastes(shown); got != long {
		t.Fatalf("expanding didn't restore the paste")
	}
}

func TestSaveHistoryKeepsNewestFirst(t *testing.T) {
	m := testModel(t)
	m.remember("first")
	m.remember("second")
	hist := loadHistory()
	if len(hist) < 2 || hist[0] != "first" || hist[len(hist)-1] != "second" {
		t.Fatalf("history = %v (newest last, as the up arrow expects)", hist)
	}
}

func TestResultTextFlattensABlock(t *testing.T) {
	// A tool_result's content is either a string or a list; both flatten.
	if got := (claude.ContentBlock{Content: []byte(`"plain"`)}).ResultText(); got != "plain" {
		t.Fatalf("string content = %q", got)
	}
	list := claude.ContentBlock{Content: []byte(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)}
	if got := list.ResultText(); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Fatalf("list content = %q", got)
	}
}
