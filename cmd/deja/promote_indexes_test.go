package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// promote says the note outranks the transcript in recall. It has to be in the
// index for that to be true, and it was not: the hook never builds, so a
// decision recorded here was invisible to the next session until some other
// command happened to run (#910).
func TestPromoteLeavesTheNoteInTheIndex(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the winch brake pads squeal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"p1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "p1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if n := index.HarnessSessionCounts(dir)["deja"]; n != 0 {
		t.Fatalf("the store already had %d note sessions", n)
	}

	if _, err := captureRun(t, "promote", "p1", "--note", "a decision I just made"); err != nil {
		t.Fatal(err)
	}
	if n := index.HarnessSessionCounts(dir)["deja"]; n != 1 {
		t.Errorf("the note is not in the index: %d note sessions", n)
	}
	// And it can be recalled without anything else running first.
	ss, err := index.Search(dir, query.Options{Query: "a decision I just made"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "a decision I just made") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the note is in the manifest but not searchable")
	}
}
