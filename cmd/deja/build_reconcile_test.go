package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Per-harness build lines count transcripts. Once any of them share an id the
// lines add up to more than the index holds, and the line that would settle it
// was TTY-only — not where anyone counting is looking (#1091).
func TestRebuildReconcilesWhenTranscriptsCollapse(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	const turn = `{"type":"user","sessionId":"dup1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the pool cap decision"}}`
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "a.jsonl"), "dup1", []string{turn})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "b.jsonl"), "dup1", []string{turn})

	out, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "an id with another transcript") {
		t.Fatalf("fixture did not collapse anything:\n%s", out)
	}
	re := regexp.MustCompile(`deja: indexed (\d+) sessions?, (\d+) messages?`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("a build that merged rows never says what the index actually holds:\n%s", out)
	}

	// The reconciling numbers must be the smaller, post-merge ones.
	perHarness := regexp.MustCompile(`deja: \w+: (\d+) sessions?, (\d+) messages?`).FindAllStringSubmatch(out, -1)
	sumSessions := 0
	for _, p := range perHarness {
		n, _ := strconv.Atoi(p[1])
		sumSessions += n
	}
	indexed, _ := strconv.Atoi(m[1])
	if indexed >= sumSessions {
		t.Errorf("reconciling line says %d sessions, per-harness lines sum to %d — expected the smaller number:\n%s",
			indexed, sumSessions, out)
	}
}
