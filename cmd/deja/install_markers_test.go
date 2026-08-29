package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every config an installer writes has to be recognisable as deja's own, or the
// uninstall cannot take back the snapshot it made of it (#2575) and the reader
// keeps a file naming a binary they removed. The marker list is written by hand
// against twenty installers, so it is checked by installing all of them rather
// than by reading it: zed and dsh were missing, and after them codex, grok and
// aider (#2578). A new harness now fails here until its spelling is known.
func TestEveryInstalledConfigIsRecognisableAsDejas(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	// This test installs; a wrong home would rewire the machine it runs on.
	if home == "" || !strings.Contains(home, "TestEveryInstalledConfig") {
		t.Fatalf("refusing to install into %q", home)
	}
	seen := map[string]bool{}
	walk := func(fn func(path string, body []byte)) {
		_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
			if err != nil || !fi.Mode().IsRegular() {
				return nil
			}
			// deja's own state and guidance are not a harness config.
			if strings.Contains(p, filepath.Join(".config", "deja")) {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			fn(p, b)
			return nil
		})
	}
	walk(func(p string, _ []byte) { seen[p] = true })

	for _, target := range []string{
		"claude-code", "codex", "opencode", "cursor", "gemini", "antigravity", "qwen", "kimi",
		"hermes", "pi", "omp", "deepseek", "openclaw", "cline", "goose", "grok", "copilot",
		"roo", "aider", "zed",
	} {
		if _, err := captureRun(t, "install", target, "--no-index", "--no-guidance"); err != nil {
			t.Fatalf("install %s: %v", target, err)
		}
	}

	written := 0
	walk(func(p string, b []byte) {
		if seen[p] {
			return
		}
		written++
		if !mentionsDeja(b) {
			line := strings.Join(strings.Fields(string(b)), " ")
			if len(line) > 120 {
				line = line[:120] + "…"
			}
			t.Errorf("%s is deja's own wiring and mentionsDeja does not know it — an uninstall will leave its backup behind:\n  %s",
				strings.TrimPrefix(p, home), line)
		}
	})
	// The premise: the installers really did write something here.
	if written < 10 {
		t.Fatalf("only %d configs were written, so this measures nothing", written)
	}
}
