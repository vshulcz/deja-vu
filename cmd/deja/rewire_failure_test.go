package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The refresh a session start runs skips a target it cannot rewrite, which is
// right — a damaged config is not a thing to overwrite. Stamping the record as
// though it had been rewritten is not: nothing retries afterwards, and doctor's
// stale-wiring check reads that record rather than the configs, so it goes
// quiet too (#2212).
func TestAFailedRewireIsNotRecordedAsDone(t *testing.T) {
	hermeticEnv(t)
	claudeJSON := sources.ClaudeJSONPath()
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("claude", "/old/path/deja", false); err != nil {
		t.Fatal(err)
	}
	recordWiring([]string{"claude"}, false)
	before, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the config names the old binary, so a rewire has work to do.
	if !strings.Contains(string(before), "/old/path/deja") {
		t.Fatalf("the install did not write the path, so this measures nothing:\n%s", before)
	}

	st := readWiringState()
	st.Version, st.Exe = "0.0.1", "/old/path/deja"
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// Damaged the way an interrupted write leaves it.
	if err := os.WriteFile(claudeJSON, before[:len(before)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	if changed := refreshWiringAfterUpgrade(); len(changed) != 0 {
		t.Fatalf("a damaged config was reported rewired: %v", changed)
	}
	after := readWiringState()
	if after.Version == version {
		t.Errorf("the record says version %q, the one now running, though nothing was rewritten", after.Version)
	}
	if after.Exe != "/old/path/deja" {
		t.Errorf("the record names %q; the configs still name /old/path/deja, which is what doctor reads", after.Exe)
	}

	// The damage is not repeated, and the next start tries again.
	now, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(now) != len(before)/2 {
		t.Errorf("the damaged config was rewritten (%d bytes, was %d)", len(now), len(before)/2)
	}
	if err := os.WriteFile(claudeJSON, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if changed := refreshWiringAfterUpgrade(); len(changed) == 0 {
		t.Errorf("the start after the file was repaired did not try again")
	}
	fixed, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fixed), "/old/path/deja") {
		t.Errorf("the repaired config still names the old binary:\n%s", fixed)
	}
}
