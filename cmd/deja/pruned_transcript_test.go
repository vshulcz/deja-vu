package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A harness that prunes an old transcript is the ordinary case — Claude Code
// does it to everything older than cleanupPeriodDays, 30 by default — and
// deja's answer to it is to keep what it indexed: the file is gone, the
// session is not, and `deja forget` is the way to drop one on purpose. Only a
// store that goes away whole leaves the index; a file on a volume that is not
// mounted (#900) or a tree that was renamed were already kept.
//
// Pinned rather than argued: this is a decision about what deja is for, made
// on purpose on 2026-09-02 (#2970) after the index had followed the file out
// the door since the first release.
func TestAPrunedTranscriptStaysInTheIndexAndItsNeighbourToo(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"s1", "s2"} {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "the pool timed out during the migration in " + sid},
		})
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	// The premise: both are there to begin with.
	if out, _ := captureRun(t, "search", "migration"); !strings.Contains(out, "s1") || !strings.Contains(out, "s2") {
		t.Fatalf("both sessions should be indexed, so this measures nothing:\n%s", out)
	}

	if err := os.Remove(filepath.Join(root, "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	// The pass says the file went and the session did not, and does not
	// claim the store is gone: this is a prune, not an unplugged disk.
	if !strings.Contains(said, "no longer on disk") || !strings.Contains(said, "still searchable") {
		t.Errorf("the pass does not say what it kept:\n%s", said)
	}
	if strings.Contains(said, "is gone, and") {
		t.Errorf("a pruned file was reported as a store that went away whole:\n%s", said)
	}

	out, _ := captureRun(t, "search", "migration")
	if !strings.Contains(out, "s1") {
		t.Errorf("the pruned session left the index with its file:\n%s", out)
	}
	if !strings.Contains(out, "s2") {
		t.Errorf("the session whose transcript is still there went too:\n%s", out)
	}

	// And nothing keeps naming the file: doctor counts what is there.
	doc, _ := captureRun(t, "doctor")
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "s1.jsonl") {
			t.Errorf("doctor still names a file nobody can act on: %q", strings.TrimSpace(line))
		}
	}
	// The store's own row, whole, so the count cannot be read out of a larger
	// number somewhere else on the screen.
	if !strings.Contains(doc, "(1 file, 2 indexed sessions, 1 from a transcript no longer on disk)") {
		t.Errorf("doctor does not say that one of the two sessions has no file any more:\n%s", doc)
	}
}
