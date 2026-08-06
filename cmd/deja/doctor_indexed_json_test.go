package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The printed doctor line has carried files and indexed sessions together
// since #861, because that pair is how a collapsed store shows. --json carried
// only the files, so a script could not make the same comparison (#1092).
func TestDoctorJSONCarriesIndexedSessions(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two files, one conversation: same sessionId, different filenames.
	const turn = `{"type":"user","sessionId":"dup1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"the pool cap decision"}}`
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "a.jsonl"), "dup1", []string{turn})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-dup", "b.jsonl"), "dup1", []string{turn})
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name            string `json:"name"`
			Files           int    `json:"files"`
			IndexedSessions int    `json:"indexed_sessions"`
		} `json:"stores"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	var found bool
	for _, s := range report.Stores {
		if s.Name != "claude" {
			continue
		}
		found = true
		if s.IndexedSessions == 0 {
			t.Errorf("claude store reports %d files and no indexed_sessions — the collapse is invisible to a reader of --json", s.Files)
		}
		if s.Files <= s.IndexedSessions {
			t.Errorf("fixture did not collapse: %d files, %d sessions", s.Files, s.IndexedSessions)
		}
	}
	if !found {
		t.Fatalf("no claude store in the report:\n%s", out)
	}
}
