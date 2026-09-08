package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// grok-auto discarded the MCP half's result, so it reported GROK.md and the
// hook file while it had also written config.toml and user-settings.json — the
// shape #3185 fixed for gemini-auto (#3220).
func TestGrokAutoNamesBothHalves(t *testing.T) {
	hermeticEnv(t)
	out := captureStdout(t, func() {
		if err := runInstall(index.DefaultDir(), []string{"grok-auto", "--no-index"}, false); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"config.toml", "hooks/deja.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %s:\n%s", want, out)
		}
	}
}
