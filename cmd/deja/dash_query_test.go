package main

import (
	"io"
	"strings"
	"testing"
)

// A question that begins with a dash reads as a flag, and the three verbs that
// refuse it said so without saying what to do instead. `--` has worked all
// along — the Amp plugin has used it since it was written — so the error names
// it now (#3395).
func TestADashLeadingQueryIsToldAboutTheSeparator(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"ctx", func() error { return cmdCtx(dir, []string{"--auto looks for the six harnesses"}) }},
		{"how", func() error { return runHow(dir, []string{"--auto looks for the six harnesses"}, io.Discard) }},
	} {
		err := c.run()
		if err == nil {
			t.Errorf("%s: a dash-leading query was accepted; the test no longer covers the message", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "goes after `--`") {
			t.Errorf("%s: %v — the message does not say a dash-leading query goes after `--`", c.name, err)
		}
	}
}
