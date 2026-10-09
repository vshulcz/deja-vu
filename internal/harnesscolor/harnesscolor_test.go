package harnesscolor

import "testing"

func TestNamedColoursMatchAcrossTerminalAndPage(t *testing.T) {
	cases := map[string]string{"claude": "#ff8700", "codex": "#5fd75f", "cursor": "#5fd7d7"}
	for h, want := range cases {
		if got := Hex(h); got != want {
			t.Errorf("Hex(%q) = %s, want %s", h, got, want)
		}
	}
	if got := Tag("claude", true); got != "\x1b[38;5;208m[claude]\x1b[0m" {
		t.Errorf("Tag = %q", got)
	}
	if got := Tag("claude", false); got != "[claude]" {
		t.Errorf("plain Tag = %q", got)
	}
}

func TestUnnamedHarnessGetsAStableColourOutsideTheNamedSet(t *testing.T) {
	taken := map[int]bool{}
	for _, n := range named {
		taken[n] = true
	}
	for _, h := range []string{"zed", "kiro", "goose", "copilot-chat", "hermes"} {
		a, b := Index(h), Index(h)
		if a != b {
			t.Fatalf("%s: unstable colour", h)
		}
		if taken[a] {
			t.Errorf("%s got a named harness's colour %d", h, a)
		}
	}
}
