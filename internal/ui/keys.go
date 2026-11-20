package ui

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// Bubble Tea v1 reads legacy key encodings only, where shift+enter is just
// enter. The kitty keyboard protocol (Ghostty, kitty, WezTerm, iTerm2, foot)
// tells them apart. Its "disambiguate" level re-encodes only the ambiguous
// keys (esc, ctrl/alt combos, modified enter/tab/backspace) as CSI u
// sequences; everything else stays as it was. Bubble Tea passes those
// through as unknown CSI messages, and csiKey turns them back into KeyMsgs.
const (
	kittyKeysOn  = "\x1b[=1;1u" // set flags to "disambiguate" (no push, so no stack to unwind)
	kittyKeysOff = "\x1b[=0;1u"
	// Many terminals turn the wheel into ↑ and ↓ on the alternate screen when
	// they aren't sending mouse events, which would recall earlier prompts
	// into the input instead of scrolling. Off while teveus runs.
	wheelKeysOff = "\x1b[?1007l"
	wheelKeysOn  = "\x1b[?1007h"
)

// setKittyKeys turns the protocol on or off, when the app owns a terminal.
func (m *Model) setKittyKeys(on bool) {
	if m.cfg.KeyOut == nil {
		return
	}
	if on {
		fmt.Fprint(m.cfg.KeyOut, kittyKeysOn+wheelKeysOff)
	} else {
		fmt.Fprint(m.cfg.KeyOut, kittyKeysOff+wheelKeysOn)
	}
}

// csiBytes returns the raw sequence of Bubble Tea's unexported
// unknownCSISequenceMsg.
func csiBytes(msg tea.Msg) ([]byte, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Type().Name() != "unknownCSISequenceMsg" || v.Kind() != reflect.Slice {
		return nil, false
	}
	return v.Bytes(), true
}

// newlineKey is what shift+enter and ctrl+enter become: the textarea's
// newline binding.
var newlineKey = tea.KeyMsg{Type: tea.KeyCtrlJ}

// csiKey decodes a kitty "CSI code[:alt] ; mods u" key, the xterm
// modifyOtherKeys form "CSI 27 ; mods ; code ~", and the CSI letter forms the
// kitty protocol uses for the arrows and navigation keys ("CSI 1;mods D").
// Bubble Tea decodes the plain arrows itself, but with the protocol on it
// can't tell "CSI 1;1D" from anything else and hands the whole sequence up:
// dropping those left the arrow keys doing nothing in the input.
func csiKey(seq []byte) (tea.KeyMsg, bool) {
	s := string(seq)
	if !strings.HasPrefix(s, "\x1b[") || len(s) < 3 {
		return tea.KeyMsg{}, false
	}
	if k, ok := csiLetterKey(s); ok {
		return k, true
	}
	if len(s) < 4 {
		return tea.KeyMsg{}, false
	}
	params := strings.Split(s[2:len(s)-1], ";")
	field := func(i, def int) int {
		if i >= len(params) {
			return def
		}
		n, err := strconv.Atoi(strings.SplitN(params[i], ":", 2)[0])
		if err != nil {
			return def
		}
		return n
	}
	var code, mods int
	switch s[len(s)-1] {
	case 'u':
		code, mods = field(0, -1), field(1, 1)
	case '~':
		if field(0, 0) != 27 {
			return tea.KeyMsg{}, false
		}
		code, mods = field(2, -1), field(1, 1)
	default:
		return tea.KeyMsg{}, false
	}
	if code < 0 || mods < 1 {
		return tea.KeyMsg{}, false
	}
	mods--
	shift, alt, ctrl := mods&1 != 0, mods&2 != 0, mods&4 != 0
	if mods&^7 != 0 {
		return tea.KeyMsg{}, false // super/hyper/meta: not ours
	}

	switch code {
	case 13:
		if shift || ctrl {
			return newlineKey, true
		}
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: alt}, true
	case 9:
		if shift {
			return tea.KeyMsg{Type: tea.KeyShiftTab, Alt: alt}, true
		}
		return tea.KeyMsg{Type: tea.KeyTab, Alt: alt}, true
	case 127, 8:
		// ctrl+backspace deletes a word, like alt+backspace.
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: alt || ctrl}, true
	case 27:
		return tea.KeyMsg{Type: tea.KeyEscape, Alt: alt}, true
	case 32:
		if ctrl {
			return tea.KeyMsg{Type: tea.KeyCtrlAt, Alt: alt}, true
		}
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}, Alt: alt}, true
	}
	r := rune(code)
	if ctrl {
		switch {
		case r >= 'a' && r <= 'z':
			return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(r-'a'), Alt: alt}, true
		case r == '[':
			return tea.KeyMsg{Type: tea.KeyEscape, Alt: alt}, true
		case r == '\\':
			return tea.KeyMsg{Type: tea.KeyCtrlBackslash, Alt: alt}, true
		case r == ']':
			return tea.KeyMsg{Type: tea.KeyCtrlCloseBracket, Alt: alt}, true
		case r == '@':
			return tea.KeyMsg{Type: tea.KeyCtrlAt, Alt: alt}, true
		}
		return tea.KeyMsg{}, false
	}
	if !unicode.IsPrint(r) {
		return tea.KeyMsg{}, false
	}
	if shift {
		r = unicode.ToUpper(r)
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}, true
}

// csiLetterKey decodes the CSI sequences that end in a letter: the arrows
// ("A" up, "B" down, "C" right, "D" left), Home and End ("H", "F"), and the
// shifted tab ("CSI Z"). The field before the letter is the modifier, so
// "CSI 1;5C" is ctrl+right. A bare "CSI D" is the legacy form Bubble Tea
// decodes itself, so it is left alone here.
func csiLetterKey(s string) (tea.KeyMsg, bool) {
	last := s[len(s)-1]
	if last == 'Z' {
		if s != "\x1b[Z" {
			return tea.KeyMsg{}, false
		}
		return tea.KeyMsg{Type: tea.KeyShiftTab}, true
	}
	switch last {
	case 'A', 'B', 'C', 'D', 'H', 'F':
	default:
		return tea.KeyMsg{}, false
	}
	num, mods, ok := strings.Cut(s[2:len(s)-1], ";")
	if !ok || num != "1" {
		return tea.KeyMsg{}, false
	}
	n, err := strconv.Atoi(strings.SplitN(mods, ":", 2)[0])
	if err != nil || n < 1 {
		return tea.KeyMsg{}, false
	}
	n--
	if n&^7 != 0 {
		return tea.KeyMsg{}, false // super/hyper/meta: not ours
	}
	shift, alt, ctrl := n&1 != 0, n&2 != 0, n&4 != 0

	// Home and End have no modified form of their own, so a modifier leaves
	// them as they are rather than turning them into an arrow.
	if last == 'H' {
		return tea.KeyMsg{Type: tea.KeyHome, Alt: alt}, true
	}
	if last == 'F' {
		return tea.KeyMsg{Type: tea.KeyEnd, Alt: alt}, true
	}
	var typ tea.KeyType
	switch last {
	case 'A':
		typ = tea.KeyUp
	case 'B':
		typ = tea.KeyDown
	case 'C':
		typ = tea.KeyRight
	default:
		typ = tea.KeyLeft
	}
	if shift {
		switch typ {
		case tea.KeyUp:
			typ = tea.KeyShiftUp
		case tea.KeyDown:
			typ = tea.KeyShiftDown
		case tea.KeyRight:
			typ = tea.KeyShiftRight
		case tea.KeyLeft:
			typ = tea.KeyShiftLeft
		}
	} else if ctrl {
		// word-wise motion in the input; Bubble Tea's own ctrl+arrow types
		// don't exist, so the same key with Alt set is what other terminals
		// send for it, and the textarea already moves by a word on those.
		alt = true
	}
	return tea.KeyMsg{Type: typ, Alt: alt}, true
}

// Long pastes collapse to a placeholder in the input, like Claude Code; the
// text goes back in when the message is sent.
const (
	pasteMaxLines = 4
	pasteMaxChars = 800
)

var pasteRef = regexp.MustCompile(`\[Pasted text #(\d+)[^\]]*\]`)

// collapsePaste stores a long paste and returns its placeholder.
func (m *Model) collapsePaste(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
	if lines < pasteMaxLines && len([]rune(text)) <= pasteMaxChars {
		return text, false
	}
	if m.pastes == nil {
		m.pastes = map[int]string{}
	}
	m.pasteN++
	m.pastes[m.pasteN] = text
	if lines > 1 {
		return fmt.Sprintf("[Pasted text #%d +%d lines]", m.pasteN, lines), true
	}
	return fmt.Sprintf("[Pasted text #%d %d chars]", m.pasteN, len([]rune(text))), true
}

// expandPastes puts the pasted text back in place of its placeholders.
func (m *Model) expandPastes(s string) string {
	if len(m.pastes) == 0 {
		return s
	}
	return pasteRef.ReplaceAllStringFunc(s, func(ref string) string {
		n, _ := strconv.Atoi(pasteRef.FindStringSubmatch(ref)[1])
		if text, ok := m.pastes[n]; ok {
			return text
		}
		return ref
	})
}
