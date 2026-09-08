package main

import (
	"path/filepath"
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
	// Through ToSlash: the report prints the host's separator, so a literal
	// "hooks/deja.json" passes everywhere and fails on the Windows leg alone.
	slashed := filepath.ToSlash(out)
	for _, want := range []string{"config.toml", "hooks/deja.json"} {
		if !strings.Contains(slashed, want) {
			t.Errorf("the report does not name %s:\n%s", want, out)
		}
	}
}
