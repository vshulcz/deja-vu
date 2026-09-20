package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// docs/INTEGRATING.md is written for people outside this repository, who cannot
// tell a command that exists from one that sounds like it should. The first
// draft of that page named three that do not — `--list`, `hook-session-start`,
// `hook-precompact` — because it was written from memory rather than from the
// binary, and an integrator following it gets "unknown command" on line one.
func TestIntegratingNamesCommandsThatExist(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "INTEGRATING.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	// Only where the page is quoting a command: inside backticks, or at the
	// start of a line in a shell block. Prose that happens to say "deja reads"
	// is not a claim about a subcommand.
	named := regexp.MustCompile("(?m)(?:^|`)deja ([a-z][a-z0-9-]*)")
	found := 0
	for _, m := range named.FindAllStringSubmatch(text, -1) {
		found++
		// dispatchKnows, not the commands map: five commands parse their own
		// arguments and live in the switch above it, `search` among them.
		if !dispatchKnows(m[1]) {
			t.Errorf("docs/INTEGRATING.md tells an integrator to run %q, which is not a command", "deja "+m[1])
		}
	}
	if found < 5 {
		t.Fatalf("only %d commands were read out of the page; the scan is wrong", found)
	}

	// The count of store variables. It moves with every harness, and the page
	// states it to say how much work DEJA_STORES replaces.
	count := regexp.MustCompile("over the (\\d+) `DEJA_").FindStringSubmatch(text)
	if count == nil {
		t.Fatal("the page no longer says how many store variables there are; if that is on purpose, this check goes with it")
	}
	said, err := strconv.Atoi(count[1])
	if err != nil {
		t.Fatal(err)
	}
	reg, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for _, m := range regexp.MustCompile(`DEJA_[A-Z0-9_]+`).FindAllString(string(reg), -1) {
		vars[m] = true
	}
	if len(vars) != said {
		t.Errorf("the page says %d store variables; the published registry names %d", said, len(vars))
	}
}
