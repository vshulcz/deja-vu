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
	"cursor":      "Cursor",
	"goose":       "Goose",
	"openclaw":    "OpenClaw",
	"hermes":      "Hermes",
	"roo":         "Roo Code",
	"omp":         "omp",
	"amp":         "Amp",
	"prime":       "prime-agent",
	"deepseek":    "DeepSeek Harness",
	"zed":         "Zed",
	"continue":    "Continue",
	"crush":       "Crush",
	"vscode":      "VS Code Copilot Chat",
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

// Cursor used to be the one the paragraph called out as skipped. It now gets a
// skill, so the general test above covers it and the paragraph has to say where
// that skill lands rather than that there is nowhere to put one.
func TestReadmeCursorGetsASkill(t *testing.T) {
	home := hermeticEnv(t)
	para := readmeGuidanceParagraph(t)
	r, err := guidanceResult("cursor", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path == "" {
		t.Fatal("cursor writes no guidance again — the README paragraph now says it does")
	}
	// Taken from the path install writes rather than pinned to a literal: this
	// test held the README to `~/.cursor/skills/` for as long as install had
	// been writing the shared directory and removing that one as retired
	// (#3187).
	dir := filepath.ToSlash(filepath.Dir(filepath.Dir(r.Path)))
	dir = "~" + strings.TrimPrefix(dir, filepath.ToSlash(filepath.Join(home, "home")))
	// The sentence about Cursor, not the paragraph: the paragraph names the
	// shared directory for Grok too, so a Cursor sentence that said nothing at
	// all would pass on someone else's words.
	sentence := ""
	for _, s := range strings.Split(para, ". ") {
		if strings.Contains(s, "Cursor has no user-level instructions file") {
			sentence = s
		}
	}
	if sentence == "" {
		t.Fatalf("the README paragraph no longer says what Cursor gets:\n%s", para)
	}
	if !strings.Contains(sentence, dir) {
		t.Errorf("README does not say Cursor's skill goes to %s:\n%s", dir, sentence)
	}
}
