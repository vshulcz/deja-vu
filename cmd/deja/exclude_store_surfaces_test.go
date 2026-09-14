package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A store the reader excluded has to read as excluded on every screen that
// mentions stores. The first pass gave `doctor` the state and left the rest
// saying what they said before: `deja sources` walked the store and reported
// three sessions from a harness deja had just been told never to open, the
// empty screens called it "no agent has run here yet", and the settings note
// counted the rule as a project pattern (#3499).
func TestEverySurfaceKnowsAStoreIsExcluded(t *testing.T) {
	tmp := hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "work", "one.jsonl"), "exc", []string{
		`{"type":"user","sessionId":"exc","timestamp":"2026-08-20T10:00:00Z","cwd":"/work/app",` +
			`"message":{"role":"user","content":"the migration ran twice"}}`,
	})
	// Wherever the rule puts it, rather than a path spelled out here: the
	// resolution reads XDG_CONFIG_HOME and then the home directory, and a test
	// that hard-codes one of those stops exercising the file at all.
	excl := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excl, []byte("harness:claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The walk read nothing, so there is nothing to answer from.
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "store not read at all") {
		t.Errorf("stats does not say a store was excluded:\n%s", out)
	}
	if strings.Contains(out, "project pattern") {
		t.Errorf("stats called the store rule a project pattern:\n%s", out)
	}

	// `deja sources` is where the empty-machine advice sends people, so it is
	// the worst place to report sessions from a store nobody reads.
	out, err = captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "claude\t") {
			continue
		}
		if !strings.Contains(line, "excluded") {
			t.Errorf("the claude row does not say excluded: %q", line)
		}
		if strings.Contains(line, "sessions=1") {
			t.Errorf("sources read a store it was told to leave alone: %q", line)
		}
	}
	if strings.Contains(out, "excluded-patterns=1") {
		t.Errorf("a store rule was counted as a project pattern:\n%s", out)
	}

	// The first screen a new reader sees must not send them looking for a
	// store that is on disk and deliberately unread.
	out, err = captureRun(t, "brief")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no agent has") {
		t.Errorf("brief blamed an empty machine for an exclusion:\n%s", out)
	}
	if !strings.Contains(out, "excluded") {
		t.Errorf("brief does not name the exclusion:\n%s", out)
	}

	// And the machine-readable form, for whatever watches it.
	out, err = captureRun(t, "doctor", "--json", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"stores"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	found := false
	for _, s := range report.Stores {
		if s.Name != "claude" {
			continue
		}
		found = true
		if s.State != "excluded" {
			t.Errorf("doctor --json says claude is %q, want excluded", s.State)
		}
	}
	if !found {
		t.Error("doctor --json dropped the excluded store instead of naming it")
	}
}
