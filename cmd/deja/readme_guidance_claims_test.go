package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readmeGuidanceNames maps an install target that writes a user-level guidance
// file to the name the README calls it by. A new guidance-writing target fails
// this test until it is added here, which is the point: the README paragraph
// named six of the eleven, so `deja install --all` wrote into ~/.roo/rules/ and
// ~/.pi/agent/skills/ with nothing in the docs saying so (#1104).
var readmeGuidanceNames = map[string]string{
	"claude-code": "Claude Code",
	"codex":       "Codex",
	"opencode":    "opencode",
	"gemini":      "Gemini CLI",
	"antigravity": "Antigravity",
	"qwen":        "Qwen",
	"kimi":        "Kimi Code",
	"pi":          "pi",
	"copilot":     "Copilot",
	"roo":         "Roo Code",
	// Grok is named in its own sentence in the same paragraph, because the
	// home copy only applies when a project has no .grok/GROK.md.
	"grok": "Grok",
}

func readmeGuidanceParagraph(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, para := range strings.Split(string(b), "\n\n") {
		if strings.Contains(para, "Install also writes user-level guidance") {
			return para
		}
	}
	t.Fatal("README no longer has the user-level guidance paragraph")
	return ""
}

func TestReadmeNamesEveryHarnessThatGetsGuidance(t *testing.T) {
	hermeticEnv(t)
	para := readmeGuidanceParagraph(t)
	writes := 0
	seen := map[string]bool{}
	for _, target := range installTargetNames() {
		// -auto targets share the guidance file of the plain target.
		harness := guidanceHarness(target)
		if guidancePath(harness) == "" || seen[harness] {
			continue
		}
		seen[harness] = true
		name, ok := readmeGuidanceNames[harness]
		if !ok {
			t.Errorf("install target %q writes user-level guidance and no README name is recorded for it", harness)
			continue
		}
		writes++
		// Word-bounded: "pi" is inside "skipped" in this very paragraph.
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(para) {
			t.Errorf("README does not name %q (install target %q), which gets a guidance file:\n%s", name, harness, para)
		}
	}
	if writes < len(readmeGuidanceNames) {
		t.Errorf("only %d of %d recorded harnesses still write guidance — the README list is now too long", writes, len(readmeGuidanceNames))
	}
}

// Cursor is the one the paragraph calls out as skipped; if it ever starts
// writing guidance the sentence becomes wrong in the other direction.
func TestReadmeCursorGuidanceExclusion(t *testing.T) {
	hermeticEnv(t)
	para := readmeGuidanceParagraph(t)
	r, err := guidanceResult("cursor", false)
	if err != nil {
		t.Fatal(err)
	}
	skipped := r.Path == ""
	says := strings.Contains(para, "Cursor has no documented user-level guidance location and is skipped")
	if skipped != says {
		t.Errorf("cursor guidance path %q vs README claim of skipping %v", r.Path, says)
	}
}
