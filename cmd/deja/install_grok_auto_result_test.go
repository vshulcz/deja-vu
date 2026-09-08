package main

import (
	"strings"
	"testing"
)

// grok-auto writes config.toml and user-settings.json through installGrok and
// the hooks file through installGrokAuto; the first result was dropped, so the
// line named only the hooks (#3220).
func TestGrokAutoResultNamesEveryFileItWrites(t *testing.T) {
	hermeticEnv(t)
	res, err := installTarget("grok-auto", "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	all := res.Path + " " + res.Note
	for _, want := range []string{"deja.json", "config.toml"} {
		if !strings.Contains(all, want) {
			t.Fatalf("install did not name %s: path=%q note=%q", want, res.Path, res.Note)
		}
	}
}
