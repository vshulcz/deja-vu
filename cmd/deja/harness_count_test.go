package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The count is spelled by hand wherever deja introduces itself, and every one
// of those places was written when it was true: the first-run screen said
// eighteen, four extensions said twenty, a plugin listing said twenty-two,
// while the registry had twenty-five (#3385). brief.go counts the registry
// now; these files cannot, so they are checked against it.
//
// The rule is the one the sentences use: the names in the phrase plus the
// number after "and" is every harness deja reads.
func TestTheIntroductionsCountEveryHarness(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	// "and <count> more", with the agents it names in the words just before it.
	phrase := regexp.MustCompile(`and[\s\n]+([a-z-]+)[\s\n]+more`)
	names := regexp.MustCompile(`Claude Code|Codex|Cursor|opencode|Gemini|Copilot|Zed`)
	files := []string{
		"extensions/zed/README.md",
		"extensions/kimi/README.md",
		"extensions/grok/README.md",
		"extensions/dsh/index.js",
		"extensions/opencode/index.js",
		"extensions/grok/.grok-plugin/plugin.json",
		"extensions/kimi/kimi.plugin.json",
		// The manifests a plugin listing renders, which live at the repo root.
		"kimi.plugin.json",
		"plugin.json",
		"gemini-extension.json",
	}
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		text := string(b)
		for _, loc := range phrase.FindAllStringSubmatchIndex(text, -1) {
			said := text[loc[2]:loc[3]]
			// Only the sentence the phrase sits in: a file that says "opencode"
			// in its own header is not naming it as one of the agents here.
			from := loc[0] - 100
			if from < 0 {
				from = 0
			}
			named := len(names.FindAllString(text[from:loc[0]], -1))
			if named == 0 {
				continue
			}
			checked++
			if want := spelledCount(n - named); said != want {
				t.Errorf("%s names %d agents and says %q more; the registry has %d, so it is %q",
					f, named, said, n, want)
			}
		}
	}
	if checked < len(files) {
		t.Errorf("only %d of %d introductions were checked — the phrasing changed and this test stopped seeing them",
			checked, len(files))
	}
}

// And the one that is computed rather than typed.
func TestTheFirstRunScreenCountsTheRegistry(t *testing.T) {
	n := registryHarnessCount(t, filepath.Join("..", ".."))
	if harnessCount() != n {
		t.Errorf("the binary counts %d harnesses; docs/registry has %d", harnessCount(), n)
	}
	if got, want := spelledCount(harnessCount()-6), fmt.Sprint(spelledCount(n-6)); got != want {
		t.Errorf("the first-run screen would say %q, want %q", got, want)
	}
	if strings.Contains(spelledCount(harnessCount()-6), " ") {
		t.Errorf("the count is not one word: %q", spelledCount(harnessCount()-6))
	}
}
