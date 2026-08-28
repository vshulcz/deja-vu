package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A harness that prunes an old transcript is the ordinary case, and deja's
// answer to it is eviction: the index mirrors the stores, and what is kept back
// is only the file on a volume that is not mounted (#900) or a tree that was
// renamed. The tests beside this one cover those two; nothing covered the plain
// one — one file deleted from a store that is otherwise still there — which is
// the case a person meets when a harness rotates its history or they tidy up.
//
// Pinned rather than argued: a change of mind here is a decision about what
// deja is for, and it should be made on purpose rather than drift in.
func TestAPrunedTranscriptLeavesTheIndexAndItsNeighbourStays(t *testing.T) {
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
	// The pass says one file went, and does not claim the store is gone: this
	// is a prune, not an unplugged disk.
	if !strings.Contains(said, "removed_files=1") {
		t.Errorf("the pass does not report the file that went:\n%s", said)
	}
	if strings.Contains(said, "is gone, and") {
		t.Errorf("a pruned file was reported as a store that went away whole:\n%s", said)
	}

	out, _ := captureRun(t, "search", "migration")
	if strings.Contains(out, "s1") {
		t.Errorf("the pruned session is still answered from the index:\n%s", out)
	}
	if !strings.Contains(out, "s2") {
		t.Errorf("the session whose transcript is still there went with it:\n%s", out)
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
	if !strings.Contains(doc, "(1 file, 1 indexed session)") {
		t.Errorf("doctor does not count the one file and session that are left:\n%s", doc)
	}
}
