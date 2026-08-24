package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A status file exists before its phase is written, and every agent-facing
// sentence interpolates progress() into parentheses — so that window read
// "indexing this machine's history ()". line() has always substituted a word
// there; progress() did not (#1731).
func TestBuildingSentenceNeverShowsAnEmptyPhase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	write := func(st warmupStatus) {
		st.Updated = time.Now().UnixNano()
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, st := range []warmupStatus{{}, {Done: 3, Total: 10}} {
		write(st)
		got := buildingNowForAgent(dir)
		if got == "" {
			t.Fatalf("no sentence at all for %+v", st)
		}
		if strings.Contains(got, "()") || strings.Contains(got, "( ") {
			t.Errorf("%+v renders an empty phase: %q", st, got)
		}
	}

	// The phase, when there is one, still reads as before.
	write(warmupStatus{Phase: "finding transcripts", Done: 1, Total: 4})
	if got := buildingNowForAgent(dir); !strings.Contains(got, "finding transcripts 25%") {
		t.Errorf("a real phase went missing: %q", got)
	}
}
