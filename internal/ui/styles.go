package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a complete palette. Backgrounds are left to the terminal except
// for diff lines, pills, user turns and the selected row so frames stay tiny.
type Theme struct {
	Name      string
	Dark      bool
	Accent    string
	Accent2   string
	Text      string
	Dim       string
	Faint     string
	Border    string // quiet hairlines for dividers and resting frames
	Surface   string // user prompt cards, soft containers
	SurfaceHi string // elevated badges, inline code chips, keycaps, search bar
	Green     string
	Red       string
	Yellow    string
	Blue      string
	DiffAdd   string
	DiffAddHi string // intra-line added token emphasis
	DiffDel   string
	DiffDelHi string // intra-line removed token emphasis
	OnAccent  string // text drawn on top of accent-coloured pills
}

var themes = []Theme{
	{Name: "claude", Dark: true, Accent: "#4FE3C1", Accent2: "#A594FF", Text: "#ECEFF4", Dim: "#8B98AA", Faint: "#4E5B6E",
		Border: "#232D3D", Surface: "#121821", SurfaceHi: "#1B2432",
		Green: "#6EE7B7", Red: "#FB7185", Yellow: "#F5C451", Blue: "#7CC7FF",
		DiffAdd: "#102921", DiffAddHi: "#1B4B39", DiffDel: "#2D141C", DiffDelHi: "#542030", OnAccent: "#05110E"},
	{Name: "minimal", Dark: true, Accent: "#7DD3FC", Accent2: "#A5B4FC", Text: "#ECEFF4", Dim: "#848D9A", Faint: "#3A424E",
		Border: "#222730", Surface: "#14181E", SurfaceHi: "#1C212A",
		Green: "#86EFAC", Red: "#FCA5A5", Yellow: "#FDE68A", Blue: "#93C5FD",
		DiffAdd: "#102820", DiffAddHi: "#1A4435", DiffDel: "#2C161D", DiffDelHi: "#4E2230", OnAccent: "#0B0F14"},
	{Name: "tokyo-night", Dark: true, Accent: "#7AA2F7", Accent2: "#BB9AF7", Text: "#C8D3F5", Dim: "#737AA2", Faint: "#444B6A",
		Border: "#292E42", Surface: "#1B1E2E", SurfaceHi: "#262B42",
		Green: "#9ECE6A", Red: "#F7768E", Yellow: "#E0AF68", Blue: "#7DCFFF",
		DiffAdd: "#1B2E24", DiffAddHi: "#284B36", DiffDel: "#331D29", DiffDelHi: "#55283C", OnAccent: "#13141C"},
	{Name: "catppuccin", Dark: true, Accent: "#CBA6F7", Accent2: "#F5C2E7", Text: "#CDD6F4", Dim: "#8C92AC", Faint: "#585B70",
		Border: "#313244", Surface: "#232334", SurfaceHi: "#313244",
		Green: "#A6E3A1", Red: "#F38BA8", Yellow: "#F9E2AF", Blue: "#89B4FA",
		DiffAdd: "#1E3328", DiffAddHi: "#2E523D", DiffDel: "#38212E", DiffDelHi: "#5B2D41", OnAccent: "#181825"},
	{Name: "gruvbox", Dark: true, Accent: "#FE8019", Accent2: "#D3869B", Text: "#EBDBB2", Dim: "#A89984", Faint: "#665C54",
		Border: "#3C3836", Surface: "#282625", SurfaceHi: "#3C3836",
		Green: "#B8BB26", Red: "#FB4934", Yellow: "#FABD2F", Blue: "#83A598",
		DiffAdd: "#2B3018", DiffAddHi: "#444D22", DiffDel: "#381C1B", DiffDelHi: "#5E2825", OnAccent: "#1D2021"},
	// High contrast: pure white text and saturated accents for low vision
	// or bright rooms.
	{Name: "high-contrast", Dark: true, Accent: "#FFB86B", Accent2: "#7FD7FF", Text: "#FFFFFF", Dim: "#D4D4D4", Faint: "#8A8A8A",
		Border: "#525252", Surface: "#1E1E1E", SurfaceHi: "#303030",
		Green: "#5CFF8A", Red: "#FF6B6B", Yellow: "#FFE45C", Blue: "#6BB8FF",
		DiffAdd: "#0F3D1E", DiffAddHi: "#1B6B34", DiffDel: "#471010", DiffDelHi: "#7A1B1B", OnAccent: "#000000"},
	{Name: "light", Dark: false, Accent: "#C15F3C", Accent2: "#6D4AFF", Text: "#1F1C19", Dim: "#6F665E", Faint: "#ACA39A",
		Border: "#D9D1C7", Surface: "#F3EEE7", SurfaceHi: "#E6DFD6",
		Green: "#227A4B", Red: "#C0392B", Yellow: "#9E6300", Blue: "#235FA4",
		DiffAdd: "#DFF3E5", DiffAddHi: "#B9E5C7", DiffDel: "#FBE2DE", DiffDelHi: "#F4BDB5", OnAccent: "#FFFFFF"},
}

func themeByName(name string) (Theme, bool) {
	for _, t := range themes {
		if t.Name == name {
			return t, true
		}
	}
	return themes[0], false
}

var (
	theme Theme

	cAccent, cAccent2, cText, cDim, cFaint, cBorder, cSurface, cSurfaceHi, cSel lipgloss.Color
	cGreen, cRed, cYellow, cBlue, cOnAccent                                     lipgloss.Color
	cDiffAdd, cDiffAddHi, cDiffDel, cDiffDelHi                                  lipgloss.Color

	sText, sDim, sFaint, sBorder, sAccent, sAccent2, sBold lipgloss.Style
	sGreen, sRed, sYellow, sTool                           lipgloss.Style
	sUser, sDiffAdd, sDiffAddHi, sDiffDel, sDiffDelHi      lipgloss.Style
	sKey                                                   lipgloss.Style

	// shimmerShades holds the three pre-baked ANSI templates for the status-bar
	// sweep. Rebuilt inside applyTheme so switching themes updates the sweep
	// colour immediately while keeping per-frame animation zero-allocation.
	shimmerShades [3]string
)

func applyTheme(t Theme) {
	theme = t
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	cAccent, cAccent2, cText = c(t.Accent), c(t.Accent2), c(t.Text)
	cDim, cFaint, cBorder = c(t.Dim), c(t.Faint), c(t.Border)
	cSurface, cSurfaceHi = c(t.Surface), c(t.SurfaceHi)
	cGreen, cRed, cYellow, cBlue, cOnAccent = c(t.Green), c(t.Red), c(t.Yellow), c(t.Blue), c(t.OnAccent)
	cDiffAdd, cDiffAddHi = c(t.DiffAdd), c(t.DiffAddHi)
	cDiffDel, cDiffDelHi = c(t.DiffDel), c(t.DiffDelHi)
	cSel = mix(cSurfaceHi, cAccent, 0.16)

	fg := func(col lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(col) }
	sText, sDim, sFaint, sBorder = fg(cText), fg(cDim), fg(cFaint), fg(cBorder)
	sAccent, sAccent2 = fg(cAccent), fg(cAccent2)
	sBold = fg(cText).Bold(true)
	sGreen, sRed, sYellow = fg(cGreen), fg(cRed), fg(cYellow)
	sTool = fg(cBlue).Bold(true)
	sUser = lipgloss.NewStyle().
		Background(cSurfaceHi).
		Padding(0, 1).
		Foreground(cText)
	sDiffAdd = lipgloss.NewStyle().Background(cDiffAdd).Foreground(cGreen)
	sDiffAddHi = lipgloss.NewStyle().Background(cDiffAddHi).Foreground(cGreen).Bold(true)
	sDiffDel = lipgloss.NewStyle().Background(cDiffDel).Foreground(cRed)
	sDiffDelHi = lipgloss.NewStyle().Background(cDiffDelHi).Foreground(cRed).Bold(true)
	sKey = lipgloss.NewStyle().Background(cSurfaceHi).Foreground(cText).Bold(true).Padding(0, 1)

	peak := lipgloss.Color("#FFFFFF")
	if !t.Dark {
		peak = lipgloss.Color("#1A1412")
	}
	for d := 0; d < 3; d++ {
		shimmerShades[d] = lipgloss.NewStyle().
			Foreground(mix(cAccent, peak, 0.65-float64(d)*0.22)).
			Render("\x00")
	}
}

func init() { applyTheme(themes[0]) }

// modeColor gives each permission mode a distinct identity, used for the
// input border and the mode pill so the current mode is always visible.
func modeColor(mode string) lipgloss.Color {
	switch mode {
	case "acceptEdits":
		return cGreen
	case "plan":
		return cBlue
	case "auto":
		return cAccent2
	case "bypassPermissions", "dontAsk":
		return cRed
	}
	return cAccent
}

// modeLabel names permission modes in plain words.
func modeLabel(mode string) string {
	switch mode {
	case "acceptEdits":
		return "auto-edit"
	case "plan":
		return "plan only"
	case "auto":
		return "autopilot"
	case "bypassPermissions":
		return "no limits"
	case "dontAsk":
		return "don't ask"
	}
	return "ask first"
}

func modeHelp(mode, engine string) string {
	switch mode {
	case "acceptEdits":
		return "file edits run without asking"
	case "plan":
		return "read-only: the agent plans, no changes"
	case "auto":
		if engine == "api" {
			return "every tool runs without asking"
		}
		return "Claude decides what is safe to run"
	case "bypassPermissions":
		return "everything runs without asking"
	}
	return "asks before edits and commands"
}

func pill(text string, bg lipgloss.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(cOnAccent).Bold(true).Padding(0, 1).Render(text)
}

// chip renders a quiet elevated badge (engine pill, stack tag, saver tag).
func chip(text string, fg lipgloss.Color) string {
	return lipgloss.NewStyle().Background(cSurfaceHi).Foreground(fg).Padding(0, 1).Render(text)
}

// softPill renders a low-glare tinted status pill (e.g. active saver, exit code).
func softPill(text string, bg, fg lipgloss.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Padding(0, 1).Render(text)
}

// key renders a keycap such as "ctrl+k".
func keycap(k string) string { return sKey.Render(k) }

// hints renders "key label" pairs for the status bar, kept quiet so the
// conversation stays the visual focus.
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sDim.Render(pairs[i])+" "+sFaint.Render(pairs[i+1]))
	}
	return strings.Join(parts, sFaint.Render("  ·  "))
}

// gradient colours each rune of s along a line from one hex colour to another.
func gradient(s string, from, to lipgloss.Color, bold bool) string {
	runes := []rune(s)
	r1, g1, b1 := hexRGB(string(from))
	r2, g2, b2 := hexRGB(string(to))
	var sb strings.Builder
	for i, r := range runes {
		t := 0.0
		if len(runes) > 1 {
			t = float64(i) / float64(len(runes)-1)
		}
		st := lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("#%02X%02X%02X",
			lerp(r1, r2, t), lerp(g1, g2, t), lerp(b1, b2, t))))
		if bold {
			st = st.Bold(true)
		}
		sb.WriteString(st.Render(string(r)))
	}
	return sb.String()
}

func hexRGB(h string) (int, int, int) {
	var r, g, b int
	fmt.Sscanf(strings.TrimPrefix(h, "#"), "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

func lerp(a, b int, t float64) int { return a + int(float64(b-a)*t) }

// mix blends two hex colours; used for the shimmer highlight.
func mix(a, b lipgloss.Color, t float64) lipgloss.Color {
	r1, g1, b1 := hexRGB(string(a))
	r2, g2, b2 := hexRGB(string(b))
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", lerp(r1, r2, t), lerp(g1, g2, t), lerp(b1, b2, t)))
}

// bar draws a gradient meter of the given width for a 0..1 fraction.
func bar(frac float64, width int) string {
	frac = max(0, min(frac, 1))
	width = max(width, 1)
	filled := int(frac*float64(width) + 0.5)
	to := cAccent2
	if frac > 0.8 {
		to = cRed
	}
	return gradient(strings.Repeat("━", filled), cAccent, to, false) +
		sFaint.Render(strings.Repeat("━", width-filled))
}
