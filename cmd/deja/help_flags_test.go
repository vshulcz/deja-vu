package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Four flags documented in usage did not work (#623, #625, #709, #717) and six
// working flags were undocumented (#757). Both directions are mechanical, so
// check them mechanically.
func TestHelpDocumentsEveryAcceptedFlag(t *testing.T) {
	help := captureHelp(t)
	documented := map[string]bool{}
	for _, f := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(help, -1) {
		documented[f] = true
	}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	accepted := map[string][]string{}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`case "(--[a-z][a-z-]*)"`),
		// Any variable name, not just a/args[i]: install compares `arg`, and
		// its --no-guidance/--no-index slipped past the narrow patterns (#1106).
		regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\[[^]]*\])? == "(--[a-z][a-z-]*)"`),
	}
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				accepted[m[1]] = append(accepted[m[1]], path)
			}
		}
	}
	for flag, where := range accepted {
		if !documented[flag] {
			t.Errorf("%s is accepted in %v but not in `deja help`", flag, where)
		}
	}
}
