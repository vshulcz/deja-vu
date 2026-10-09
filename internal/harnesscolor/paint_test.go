package harnesscolor

import (
	"strings"
	"testing"
)

func TestPaintAndMapUseTheHarnessColour(t *testing.T) {
	if Paint("claude", "x", false) != "x" {
		t.Error("Paint without colour changed the text")
	}
	if got := Paint("claude", "x", true); !strings.HasPrefix(got, ANSI("claude")) || !strings.HasSuffix(got, "x\x1b[0m") {
		t.Errorf("Paint = %q", got)
	}
	m := Map([]string{"claude", "codex"})
	if len(m) != 2 || m["claude"] != Hex("claude") || m["codex"] != Hex("codex") {
		t.Errorf("Map = %v", m)
	}
	if hex256(5) != "#bcbcbc" || hex256(240) != "#bcbcbc" {
		t.Error("indices outside the 6x6x6 cube fall back to grey")
	}
}
