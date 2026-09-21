package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "named agents + N more" is checked on the pages a reader lands on (#3356,
// TestPageTitlesCountTheRestOfTheHarnesses) and was checked nowhere in the
// files a *store* shows: the marketplace entries, the plugin manifests and the
// per-extension package descriptions. Those had drifted furthest, because
// nothing reads them on the way past — the Claude marketplace and the Codex
// plugin said "a dozen others" of thirty-four, the Gemini extension said
// twenty-nine more of thirty, and the page explaining why agents forget
// listed six formats and "a dozen others".
//
// One table, both spellings, every file that carries the phrase. `named` is
// how many agents the sentence names before it, so the arithmetic is the one a
// reader would do.
func TestEveryStoreListingCountsTheRestOfTheHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	for _, c := range []struct {
		file  string
		named int
		shape string // %s takes the spelled number, %d the digits
		digit bool
	}{
		{file: "plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "kimi.plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "gemini-extension.json", named: 4, shape: "Gemini CLI, Claude Code, Codex, Cursor and %s more"},
		{file: ".claude-plugin/marketplace.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "codex-plugin/.codex-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "extensions/kimi/kimi.plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "extensions/grok/.grok-plugin/plugin.json", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more"},
		{file: "extensions/grok/README.md", named: 5, shape: "Claude Code, Codex, Cursor, opencode, Zed and %s more agents"},
		{file: "extensions/dsh/index.js", named: 4, shape: "Claude Code, Codex, Cursor, opencode and %s more agents"},
		{file: "extensions/opencode/index.js", named: 4, shape: "Claude Code, Codex, Cursor, Gemini and %s more agents"},
		{file: "extensions/pi/package.json", named: 4, shape: "pi, Claude Code, Codex, Cursor and %d more", digit: true},
		{file: "extensions/openclaw/package.json", named: 4, shape: "OpenClaw, Claude Code, Codex, Cursor and %d more", digit: true},
		{file: "docs/guide/forgetting.html", named: 6, shape: "Cursor, Gemini CLI, Zed and %s more agents"},
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.file)))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		rest := n - c.named
		var want string
		if c.digit {
			want = fmt.Sprintf(c.shape, rest)
		} else {
			want = fmt.Sprintf(c.shape, countWordForTest(rest))
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not say %q — the registry has %d harnesses and the sentence names %d",
				c.file, want, n, c.named)
		}
	}
}

// A listing that stopped spelling the count at all would pass the table above
// only by losing its sentence, so the phrase itself has to still be there.
func TestNoStoreListingUndercountsWithADozen(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, f := range []string{
		"plugin.json", "kimi.plugin.json", "gemini-extension.json",
		".claude-plugin/marketplace.json", "codex-plugin/.codex-plugin/plugin.json",
		"docs/guide/forgetting.html",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		// "a dozen" is how each of these went stale: a number that reads as
		// approximate and stops being true without looking wrong.
		if strings.Contains(string(b), "a dozen") {
			t.Errorf("%s says \"a dozen\" — spell the count so a test can keep it true", f)
		}
	}
}
