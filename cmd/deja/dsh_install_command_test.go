package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dsh declares --profile with Commander's requiredOption, so `dsh plugin add
// <pkg>` exits before pnpm is reached. We published the bare form in four
// places, and a broken install line is worse than no install line: it fails at
// the first thing a new user tries.
//
// https://github.com/deepseek-ai/deepseek-harness/blob/main/apps/cli/src/args.ts
func TestDshInstallCommandCarriesAProfile(t *testing.T) {
	for _, rel := range []string{
		"README.md",
		"extensions/README.md",
		"extensions/dsh/README.md",
		"docs/guide/getting-started.html",
		"docs/guide/memory-for-dsh.html",
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		text := string(b)
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, "dsh plugin") || !strings.Contains(line, "add") {
				continue
			}
			if !strings.Contains(line, "--profile") {
				t.Errorf("%s: %q has no --profile, and dsh refuses the command without it", rel, strings.TrimSpace(line))
			}
		}
	}
}
