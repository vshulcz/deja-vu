package main

import (
	"strings"
	"testing"
)

// A window of zero or less parsed, and every caller then skipped the cut it
// asked for: `--since -1d` searched the whole store and said nothing about it
// (#1610). Refusing it is the only answer that cannot be mistaken for a result.
func TestParseDurRefusesAWindowThatIsNotPositive(t *testing.T) {
	for _, in := range []string{"0d", "0", "-0d", "-1d", "-12h", "-90m"} {
		d, err := parseDur(in)
		if err == nil {
			t.Errorf("parseDur(%q) = %v with no error; a window has to be positive", in, d)
			continue
		}
		if !strings.Contains(err.Error(), in) {
			t.Errorf("parseDur(%q) does not name what was typed: %v", in, err)
		}
	}
	// The control: the windows deja documents still parse.
	for _, in := range []string{"30d", "12h", "90m", "45s"} {
		if _, err := parseDur(in); err != nil {
			t.Errorf("parseDur(%q) = %v, want a window", in, err)
		}
	}
}

// 365000d is a thousand years and the way people say "all of it". Multiplied
// out it overflows time.Duration and came back negative, so the window that was
// meant to hold everything held nothing and the filter was skipped (#1610).
func TestHugeDayWindowSaturatesInsteadOfWrapping(t *testing.T) {
	d, err := parseDur("365000d")
	if err != nil {
		t.Fatalf("parseDur(365000d) = %v", err)
	}
	if d <= 0 {
		t.Errorf("parseDur(365000d) = %v, want the largest window it can hold", d)
	}
}
