package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Everything the MCP server answers from opens with EnsureForSearchStale, which
// refuses to wait: it serves the snapshot on disk and hands the rebuild to a
// detached warmup, because rebuilding inline blows the client's tool timeout.
// Then, to print what a session concluded, it read the whole best hit through
// findByPrefix — the CLI helper, which opens with a blocking index.Ensure. The
// careful part was undone a few hundred lines below it, and if the lock was
// free the server would run the rebuild on its own thread rather than wait for
// it. Found by instrumenting LockWaitNotice and reading the stack:
//
//	index.lockDir → index.Ensure → main.findByPrefix → main.recallTextResult
//
// Asserted on the source because the behavioural version needs a second process
// holding the lock at the right instant, which is racy in a unit suite. Every
// file the server answers from is scanned, not just mcp.go: the first version
// of this guard read one of the two and would have missed the same call in
// resources/read.
func TestNothingTheMCPServerServesUsesTheBlockingLookup(t *testing.T) {
	// Guard the guard: a rename must retire or retarget this, not leave it
	// green against a name nobody uses.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "func findByPrefix(") {
		t.Fatal("findByPrefix is gone from main.go — retire or retarget this test")
	}

	files, err := filepath.Glob("mcp*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			// No trailing paren: findByPrefixHarness calls findByPrefix and
			// blocks exactly the same way, and a bare name would match an
			// alias taken as a value.
			if strings.Contains(line, "findByPrefix") {
				t.Errorf("%s:%d reaches findByPrefix, which blocks on the index lock; "+
					"this path serves a client that cannot wait", name, i+1)
			}
		}
	}
	if scanned < 2 {
		t.Fatalf("scanned %d files; the server is spread over more than that, so this is reading the wrong directory", scanned)
	}
}
