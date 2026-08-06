package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every command names an unknown flag the same way — quoted — so a reader
// scanning two refusals side by side sees one convention. Three commands
// printed it bare (`unknown flag --all-matches`) while ten quoted it.
func TestUnknownFlagIsQuotedEverywhere(t *testing.T) {
	bare := regexp.MustCompile(`unknown flag %s`)
	entries, err := os.ReadDir("./")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("./", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if loc := bare.FindIndex(b); loc != nil {
			line := 1 + strings.Count(string(b[:loc[0]]), "\n")
			t.Errorf("%s:%d prints an unknown flag unquoted; use %%q like the other commands", e.Name(), line)
		}
	}
}
