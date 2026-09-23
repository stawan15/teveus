package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a complete palette. Backgrounds are left to the terminal except
// for diff lines, pills and the selected row.
type Theme struct {
	Name     string
	Dark     bool
	Accent   string
	Accent2  string
	Text     string
	Dim      string
	Faint    string
	Surface  string // selected rows, keycaps
	Green    string
	Red      string
	Yellow   string
	Blue     string
	DiffAdd  string
	DiffDel  string
	OnAccent string // text drawn on top of accent-coloured pills
}

var themes = []Theme{
	{Name: "claude", Dark: true, Accent: "#E07A5F", Accent2: "#B48EFF", Text: "#ECE6DF", Dim: "#8C857E", Faint: "#46403B", Surface: "#2E2A27",
		Green: "#7FD1A0", Red: "#F2857A", Yellow: "#F2C66D", Blue: "#8AB4F8", DiffAdd: "#16301F", DiffDel: "#3A1B19", OnAccent: "#1A1614"},
	{Name: "tokyo-night", Dark: true, Accent: "#7AA2F7", Accent2: "#BB9AF7", Text: "#C0CAF5", Dim: "#6B7394", Faint: "#3B4261", Surface: "#292E42",
		Green: "#9ECE6A", Red: "#F7768E", Yellow: "#E0AF68", Blue: "#7DCFFF", DiffAdd: "#1F3326", DiffDel: "#3B2230", OnAccent: "#16161E"},
	{Name: "catppuccin", Dark: true, Accent: "#CBA6F7", Accent2: "#F5C2E7", Text: "#CDD6F4", Dim: "#7F849C", Faint: "#45475A", Surface: "#313244",
		Green: "#A6E3A1", Red: "#F38BA8", Yellow: "#F9E2AF", Blue: "#89B4FA", DiffAdd: "#243A2E", DiffDel: "#3E2633", OnAccent: "#1E1E2E"},
	{Name: "gruvbox", Dark: true, Accent: "#FE8019", Accent2: "#D3869B", Text: "#EBDBB2", Dim: "#928374", Faint: "#504945", Surface: "#3C3836",
		Green: "#B8BB26", Red: "#FB4934", Yellow: "#FABD2F", Blue: "#83A598", DiffAdd: "#32361A", DiffDel: "#3C1F1E", OnAccent: "#1D2021"},
	// High contrast: pure white text and saturated accents for low vision
	// or bright rooms.
	{Name: "high-contrast", Dark: true, Accent: "#FFB86B", Accent2: "#7FD7FF", Text: "#FFFFFF", Dim: "#D0D0D0", Faint: "#8A8A8A", Surface: "#303030",
		Green: "#5CFF8A", Red: "#FF6B6B", Yellow: "#FFE45C", Blue: "#6BB8FF", DiffAdd: "#0F4020", DiffDel: "#4A1010", OnAccent: "#000000"},
	{Name: "light", Dark: false, Accent: "#C15F3C", Accent2: "#7C4DFF", Text: "#2B2622", Dim: "#81776E", Faint: "#D3CBC2", Surface: "#EEE8E1",
		Green: "#2E8B57", Red: "#C0392B", Yellow: "#A86B00", Blue: "#2B6CB0", DiffAdd: "#DFF3E5", DiffDel: "#FBE2DE", OnAccent: "#FFFFFF"},
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

	cAccent, cAccent2, cText, cDim, cFaint, cSurface lipgloss.Color
	cGreen, cRed, cYellow, cBlue, cOnAccent          lipgloss.Color

	sText, sDim, sFaint, sAccent, sAccent2, sBold lipgloss.Style
	sGreen, sRed, sYellow, sTool                  lipgloss.Style
	sUser, sDiffAdd, sDiffDel, sKey, sSelected    lipgloss.Style
)

func applyTheme(t Theme) {
	theme = t
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	cAccent, cAccent2, cText, cDim, cFaint, cSurface = c(t.Accent), c(t.Accent2), c(t.Text), c(t.Dim), c(t.Faint), c(t.Surface)
	cGreen, cRed, cYellow, cBlue, cOnAccent = c(t.Green), c(t.Red), c(t.Yellow), c(t.Blue), c(t.OnAccent)

	fg := func(col lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(col) }
	sText, sDim, sFaint = fg(cText), fg(cDim), fg(cFaint)
	sAccent, sAccent2 = fg(cAccent), fg(cAccent2)
	sBold = fg(cText).Bold(true)
	sGreen, sRed, sYellow = fg(cGreen), fg(cRed), fg(cYellow)
	sTool = fg(cBlue).Bold(true)
	sUser = lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(cAccent).
		PaddingLeft(1).
		Foreground(cText)
	sDiffAdd = lipgloss.NewStyle().Background(c(t.DiffAdd)).Foreground(cGreen)
	sDiffDel = lipgloss.NewStyle().Background(c(t.DiffDel)).Foreground(cRed)
	sKey = lipgloss.NewStyle().Background(cSurface).Foreground(cText).Padding(0, 1)
	sSelected = lipgloss.NewStyle().Background(cSurface)
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

// key renders a keycap such as "ctrl+k".
func keycap(k string) string { return sKey.Render(k) }

// hints renders "key label" pairs for the status bar.
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sText.Render(pairs[i])+" "+sDim.Render(pairs[i+1]))
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
