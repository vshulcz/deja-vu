package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// importDecision puts a promoted decision through the door it really arrives
// by: a sync batch carrying the note session, whose text holds the state (the
// other machine's notes.jsonl never travels — #975).
func importDecision(t *testing.T, project, text string) {
	t.Helper()
	tmp := t.TempDir()
	rec, err := json.Marshal(index.SyncRecord{
		Harness: "deja", SessionID: "deja-note-claude-dec", Project: project,
		Role: "user", Text: "[accepted] " + text + " (from claude:dec, 2026-08-29)",
		Time: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "deja-sync-x.jsonl"), append(rec, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", tmp); err != nil {
		t.Fatal(err)
	}
}

// The block that says "follow them unless the user overrides" stopped naming a
// decision the moment it arrived by sync: it reads this machine's notes file,
// and a synced promotion is a session under `imported:<project>` (#2512).
func TestStandingDecisionsIncludeWhatArrivedBySync(t *testing.T) {
	hermeticEnv(t)
	importDecision(t, "home/app", "the retry budget stays at 5")

	out := projectConventions([]string{"home/app"}, 6, 800)
	if !strings.Contains(out, "retry budget stays at 5") {
		t.Errorf("a decision the team agreed on elsewhere is not standing here:\n%s", out)
	}
}

// Another project's decision does not become this one's.
func TestAnImportedDecisionKeepsItsScope(t *testing.T) {
	hermeticEnv(t)
	importDecision(t, "clients/acme", "the acme retry budget stays at 5")

	if out := projectConventions([]string{"home/app"}, 6, 800); strings.Contains(out, "acme retry budget") {
		t.Errorf("another project's decision reached this project's block:\n%s", out)
	}
}

// And a rule that withholds imported memory from the agent takes it back out.
func TestAWithheldImportedDecisionIsNotStanding(t *testing.T) {
	hermeticEnv(t)
	importDecision(t, "home/app", "the retry budget stays at 5")
	writePolicy(t, `{"activations":{"auto":{"imported":false}}}`)

	if out := projectConventions([]string{"home/app"}, 6, 800); strings.Contains(out, "retry budget") {
		t.Errorf("imported memory the auto rule withholds is standing anyway:\n%s", out)
	}
}
