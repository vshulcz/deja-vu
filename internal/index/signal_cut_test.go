package index

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A long tool output is stored as its head plus its tail, and both ends were
// cut at a byte count. The result is indexed and served back through recall, so
// a character split there is a broken byte inside an answer (#1319). One byte
// of padding moves the cut off the boundary it happened to land on.
func TestSignalLinesCutsOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		text := pad + strings.Repeat("сборка упала на предпродакшене ", 400)
		got := signalLines(text)
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: the stored output is not valid UTF-8", pad)
		}
		if len(got) == 0 {
			t.Errorf("pad %q: nothing was kept", pad)
		}
	}
}

// The keeping rule itself is unchanged: a short output survives whole, and a
// long one keeps both ends rather than only its head.
func TestSignalLinesStillKeepsBothEnds(t *testing.T) {
	short := "npm ERR! code ELIFECYCLE"
	if got := signalLines(short); got != short {
		t.Errorf("a short output was changed: %q", got)
	}
	head := strings.Repeat("a", signalFloor)
	tail := "the error at the very end: ELIFECYCLE"
	got := signalLines(head + strings.Repeat("b", signalTail*2) + tail)
	if !strings.Contains(got, tail) {
		t.Errorf("the tail, where errors cluster, was dropped:\n%q", got[max(0, len(got)-80):])
	}
	if !strings.HasPrefix(got, "aaa") {
		t.Errorf("the head was dropped: %q", got[:20])
	}
}
