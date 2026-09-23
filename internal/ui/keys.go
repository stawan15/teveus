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
)

// setKittyKeys turns the protocol on or off, when the app owns a terminal.
func (m *Model) setKittyKeys(on bool) {
	if m.cfg.KeyOut == nil {
		return
	}
	if on {
		fmt.Fprint(m.cfg.KeyOut, kittyKeysOn)
	} else {
		fmt.Fprint(m.cfg.KeyOut, kittyKeysOff)
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

// csiKey decodes a kitty "CSI code[:alt] ; mods u" key, or the xterm
// modifyOtherKeys form "CSI 27 ; mods ; code ~".
func csiKey(seq []byte) (tea.KeyMsg, bool) {
	s := string(seq)
	if !strings.HasPrefix(s, "\x1b[") || len(s) < 4 {
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
