package tui

import (
	"reflect"
	"testing"
)

func keyEv(k Key, r rune) Event { return Event{Kind: EvKey, Key: k, Rune: r} }

func TestDecode(t *testing.T) {
	cases := []struct {
		in   string
		want []Event
		rest string
	}{
		{"ab", []Event{keyEv(KeyRune, 'a'), keyEv(KeyRune, 'b')}, ""},
		{"é", []Event{keyEv(KeyRune, 'é')}, ""},
		{"\xc3", nil, "\xc3"},
		{"\r\n\t\x7f\x08", []Event{keyEv(KeyEnter, 0), keyEv(KeyEnter, 0), keyEv(KeyTab, 0), keyEv(KeyBackspace, 0), keyEv(KeyBackspace, 0)}, ""},
		{"\x0b\x00", []Event{keyEv(KeyCtrl, 'k'), keyEv(KeyCtrl, ' ')}, ""},
		{"\x1b[A\x1b[B\x1b[C\x1b[D\x1b[H\x1b[F\x1b[Z", []Event{keyEv(KeyUp, 0), keyEv(KeyDown, 0), keyEv(KeyRight, 0), keyEv(KeyLeft, 0), keyEv(KeyHome, 0), keyEv(KeyEnd, 0), keyEv(KeyBackTab, 0)}, ""},
		{"\x1bOA\x1bOH", []Event{keyEv(KeyUp, 0), keyEv(KeyHome, 0)}, ""},
		{"\x1bOx", nil, ""},
		{"\x1b[1~\x1b[4~\x1b[3~\x1b[5~\x1b[6~\x1b[7~\x1b[8~\x1b[9~", []Event{keyEv(KeyHome, 0), keyEv(KeyEnd, 0), keyEv(KeyDelete, 0), keyEv(KeyPgUp, 0), keyEv(KeyPgDn, 0), keyEv(KeyHome, 0), keyEv(KeyEnd, 0)}, ""},
		{"\x1b[1;5A", []Event{{Kind: EvKey, Key: KeyUp, Ctrl: true}}, ""},
		{"\x1b[1;3B", []Event{{Kind: EvKey, Key: KeyDown, Alt: true}}, ""},
		{"\x1b[1;;A", []Event{keyEv(KeyUp, 0)}, ""},
		{"\x1b[99q", nil, ""},
		{"\x1bx", []Event{{Kind: EvKey, Key: KeyRune, Rune: 'x', Alt: true}}, ""},
		{"\x1b\x1b", []Event{keyEv(KeyEsc, 0)}, "\x1b"},
		{"\x1b", nil, "\x1b"},
		{"\x1b[", nil, "\x1b["},
		{"\x1bO", nil, "\x1bO"},
		{"\x1b\xc3", nil, "\x1b\xc3"},
		{"\x1b[<0;5;3M", []Event{{Kind: EvMouse, Button: MouseLeft, X: 4, Y: 2, Press: true}}, ""},
		{"\x1b[<0;5;3m", []Event{{Kind: EvMouse, Button: MouseLeft, X: 4, Y: 2}}, ""},
		{"\x1b[<64;1;1M\x1b[<65;1;1M", []Event{{Kind: EvMouse, Button: MouseWheelUp, Press: true}, {Kind: EvMouse, Button: MouseWheelDown, Press: true}}, ""},
		{"\x1b[<16;2;2M", []Event{{Kind: EvMouse, Button: MouseLeft, X: 1, Y: 1, Press: true}}, ""},
		{"\x1b[<32;2;2M", nil, ""},
		{"\x1b[<0;2M", nil, ""},
		{"\x1b[<0;2;99999999999999999999M", nil, ""},
		{"\x1b[200~hi there\x1b[201~x", []Event{{Kind: EvPaste, Text: "hi there"}, keyEv(KeyRune, 'x')}, ""},
		{"\x1b[200~half", nil, "\x1b[200~half"},
		{"\x1c\x1d\x1f", []Event{keyEv(KeyCtrl, '\\'), keyEv(KeyCtrl, ']'), keyEv(KeyCtrl, '_')}, ""},
		{"\x1b[[A\x1b[[", nil, "\x1b[["},
		{"\x1b[3;5~\x1b[5;3~", []Event{{Kind: EvKey, Key: KeyDelete, Ctrl: true}, {Kind: EvKey, Key: KeyPgUp, Alt: true}}, ""},
	}
	for _, c := range cases {
		got, rest := Decode([]byte(c.in))
		if !reflect.DeepEqual(got, c.want) || string(rest) != c.rest {
			t.Errorf("Decode(%q) = %+v rest %q; want %+v rest %q", c.in, got, rest, c.want, c.rest)
		}
	}
}

func TestFlush(t *testing.T) {
	if got := Flush([]byte("\x1b")); len(got) != 1 || got[0].Key != KeyEsc {
		t.Errorf("a bare ESC left over is the Esc key, got %+v", got)
	}
	if got := Flush([]byte("\x1b[1")); got != nil {
		t.Errorf("a cut sequence is dropped, got %+v", got)
	}
	if got := Flush([]byte("\x1b[")); len(got) != 1 || got[0].Rune != '[' || !got[0].Alt {
		t.Errorf("ESC [ left over is alt-[, got %+v", got)
	}
	if got := Flush([]byte("\x1b[200~half")); len(got) != 1 || got[0].Kind != EvPaste || got[0].Text != "half" {
		t.Errorf("a paste that never closed is the paste so far, got %+v", got)
	}
}
