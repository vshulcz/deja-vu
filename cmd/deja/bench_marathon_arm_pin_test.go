package main

import (
	"encoding/json"
	"testing"
)

// A marathon that brushes the question across hundreds of turns and the short
// session that settled it scored alike, so on ten questions phrased from real
// PR titles the giant took the top place twice and the session that did the
// work came second (#3214). The bench arm is where that shape is reproducible:
// the ranking's own unit fixtures are too small to build a marathon out of.
func TestTheMarathonDoesNotTakeTheSpecificQuestion(t *testing.T) {
	if testing.Short() {
		t.Skip("the prompt bench builds a corpus")
	}
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "prompt", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report promptReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid prompt JSON: %v", err)
	}
	arm := report.SpecificVsMarathon
	if arm.Cases == 0 {
		t.Fatal("the arm has no cases, so this measures nothing")
	}
	if arm.FalseFires != 0 {
		t.Errorf("the marathon took %d of %d specific questions", arm.FalseFires, arm.Cases)
	}
}
