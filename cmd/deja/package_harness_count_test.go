package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The harness packages under extensions/ describe deja to npm, ClawHub and
// the pi gallery, and each says the count in its own way: "twenty-two other
// coding agents" (the host excluded), or "pi, Claude Code, Codex, Cursor and
// 19 more" (the named ones excluded). The day copilot-chat made it
// twenty-three, the seven files the README test pins were updated and these
// were not (#3099). Same rule, one directory over.
func TestPackagesCountTheOtherHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)
	other, words := harnessCountWords(t, root, -1)
	dirs, err := filepath.Glob(filepath.Join(root, "extensions", "*", "package.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no packages under extensions/: %v", err)
	}
	more := regexp.MustCompile(`([^—.:"]*?)\band (\d+) more\b`)
	for _, pkg := range dirs {
		for _, name := range []string{"package.json", "README.md"} {
			path := filepath.Join(filepath.Dir(pkg), name)
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.ToLower(string(b))
			rel, _ := filepath.Rel(root, path)
			// "N other coding agents": the count less the host.
			for _, w := range words {
				if w != other && strings.Contains(text, w+" other") {
					t.Errorf("%s says %q other agents; the registry has %d, so %q", rel, w, n, other)
				}
			}
			// "A, B, C and N more": the named ones plus N is the count.
			for _, m := range more.FindAllStringSubmatch(text, -1) {
				got, _ := strconv.Atoi(m[2])
				named := len(strings.Split(strings.TrimSpace(m[1]), ","))
				if named+got != n {
					t.Errorf("%s names %d agents and %d more, %d in all; the registry has %d", rel, named, got, named+got, n)
				}
			}
		}
	}
}
