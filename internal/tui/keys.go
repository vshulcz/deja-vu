package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key names a key that is not a printable rune.
type Key int

const (
	KeyRune Key = iota
	KeyEnter
	KeyTab
	KeyBackTab
	KeyBackspace
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
	KeyCtrl // Rune holds the letter: ctrl-k is {KeyCtrl, 'k'}
)

// Kind tells an Event's fields apart.
type Kind int

const (
	EvKey Kind = iota
	EvMouse
	EvPaste
	EvResize
)

// Mouse buttons as SGR reports them, plus the wheel.
const (
	MouseLeft      = 0
	MouseWheelUp   = 64
	MouseWheelDown = 65
)

type Event struct {
	Kind Kind
	Key  Key
	Rune rune
	Alt  bool
	Ctrl bool // a modified arrow: ctrl-up and the like

	// Mouse: zero-based cell, the button, and whether it was a press.
	X, Y   int
	Button int
	Press  bool

	Text string // paste
	W, H int    // resize
}

const pasteEnd = "\x1b[201~"

// Decode turns raw terminal input into events. rest is a trailing sequence
// that has not finished arriving; the caller prepends it to the next read.
func Decode(b []byte) (evs []Event, rest []byte) {
	for len(b) > 0 {
		ev, n, ok := decodeOne(b)
		if !ok {
			return evs, b
		}
		if n > 0 {
			b = b[n:]
		}
		if ev != nil {
			evs = append(evs, *ev)
		}
	}
	return evs, nil
}

// Flush is what a lone trailing sequence means once nothing more is coming: a
// bare ESC is the Esc key, anything else is dropped.
func Flush(rest []byte) []Event {
	if len(rest) == 1 && rest[0] == 0x1b {
		return []Event{{Kind: EvKey, Key: KeyEsc}}
	}
	return nil
}

func key(k Key) *Event { return &Event{Kind: EvKey, Key: k} }

func decodeOne(b []byte) (*Event, int, bool) {
	c := b[0]
	switch {
	case c == 0x1b:
		return decodeEsc(b)
	case c == '\r' || c == '\n':
		return key(KeyEnter), 1, true
	case c == '\t':
		return key(KeyTab), 1, true
	case c == 0x7f || c == 0x08:
		return key(KeyBackspace), 1, true
	case c == 0:
		return &Event{Kind: EvKey, Key: KeyCtrl, Rune: ' '}, 1, true
	case c < 0x20:
		return &Event{Kind: EvKey, Key: KeyCtrl, Rune: rune('a' + c - 1)}, 1, true
	}
	if !utf8.FullRune(b) {
		return nil, 0, false
	}
	r, n := utf8.DecodeRune(b)
	return &Event{Kind: EvKey, Key: KeyRune, Rune: r}, n, true
}

func decodeEsc(b []byte) (*Event, int, bool) {
	if len(b) == 1 {
		return nil, 0, false
	}
	switch b[1] {
	case '[':
		return decodeCSI(b)
	case 'O':
		if len(b) < 3 {
			return nil, 0, false
		}
		if k, ok := finalKey(b[2]); ok {
			return key(k), 3, true
		}
		return nil, 3, true
	case 0x1b:
		// ESC ESC: the first one was the key.
		return key(KeyEsc), 1, true
	}
	ev, n, ok := decodeOne(b[1:])
	if !ok {
		return nil, 0, false
	}
	if ev != nil {
		ev.Alt = true
	}
	return ev, n + 1, true
}

func finalKey(c byte) (Key, bool) {
	switch c {
	case 'A':
		return KeyUp, true
	case 'B':
		return KeyDown, true
	case 'C':
		return KeyRight, true
	case 'D':
		return KeyLeft, true
	case 'H':
		return KeyHome, true
	case 'F':
		return KeyEnd, true
	case 'Z':
		return KeyBackTab, true
	}
	return 0, false
}

func decodeCSI(b []byte) (*Event, int, bool) {
	// Parameters run until a final byte in 0x40..0x7e.
	i := 2
	for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
		i++
	}
	if i >= len(b) {
		return nil, 0, false
	}
	params, final, n := string(b[2:i]), b[i], i+1
	if strings.HasPrefix(params, "<") && (final == 'M' || final == 'm') {
		return decodeMouse(params[1:], final == 'M'), n, true
	}
	if params == "200" && final == '~' {
		end := strings.Index(string(b[n:]), pasteEnd)
		if end < 0 {
			return nil, 0, false
		}
		return &Event{Kind: EvPaste, Text: string(b[n : n+end])}, n + end + len(pasteEnd), true
	}
	if final == '~' {
		first, _, _ := strings.Cut(params, ";")
		switch first {
		case "1", "7":
			return key(KeyHome), n, true
		case "4", "8":
			return key(KeyEnd), n, true
		case "3":
			return key(KeyDelete), n, true
		case "5":
			return key(KeyPgUp), n, true
		case "6":
			return key(KeyPgDn), n, true
		}
		return nil, n, true
	}
	k, ok := finalKey(final)
	if !ok {
		return nil, n, true
	}
	ev := key(k)
	// 1;5A is ctrl-up, 1;3A alt-up.
	if _, mod, found := strings.Cut(params, ";"); found {
		if m, err := strconv.Atoi(mod); err == nil {
			m--
			ev.Alt = m&2 != 0
			ev.Ctrl = m&4 != 0
		}
	}
	return ev, n, true
}

func decodeMouse(p string, press bool) *Event {
	parts := strings.Split(p, ";")
	if len(parts) != 3 {
		return nil
	}
	btn, e1 := strconv.Atoi(parts[0])
	x, e2 := strconv.Atoi(parts[1])
	y, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return nil
	}
	// Motion and modifier bits ride on the button; keep the button itself.
	if btn&32 != 0 {
		return nil
	}
	return &Event{Kind: EvMouse, Button: btn &^ (4 | 8 | 16), X: x - 1, Y: y - 1, Press: press}
}
