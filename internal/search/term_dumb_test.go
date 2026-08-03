package search

import (
	"os"
	"testing"
)

// The search output honoured NO_COLOR and ignored TERM=dumb, so a terminal
// that cannot render escapes got them anyway (#903).
func TestColorOKHonoursDumbTerminals(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !colorOK(f) {
		t.Fatal("a character device with a real TERM lost its colour")
	}
	t.Setenv("TERM", "dumb")
	if colorOK(f) {
		t.Error("TERM=dumb still coloured")
	}
	// And the older half of the rule, which nothing covered: removing it
	// passed the whole suite.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if colorOK(f) {
		t.Error("NO_COLOR stopped being honoured")
	}
}
