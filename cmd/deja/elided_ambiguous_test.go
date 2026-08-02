package main

import (
	"github.com/vshulcz/deja-vu/internal/index"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An id copied off a result line can stand for more than one session, and the
// characters that would tell them apart are the ones the line elided — so
// "use a longer prefix" is advice the reader cannot follow, and a destructive
// command must not drop twice what they named once (#859).
func TestAnAmbiguousElidedIdIsRefusedAndExplained(t *testing.T) {
	hermeticEnv(t)
	notesDir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(notesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(notesDir, "notes.jsonl"))
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the davit ram seal"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// Two notes whose ids share a head and a tail: deja mints them as
	// deja-<date>-<project>, and "<x>-service" is an ordinary project name.
	for _, project := range []string{"super-service", "hyper-service"} {
		if _, err := captureRun(t, "remember", "note about the davit ram seal", "--project", project); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	// The elided form both notes print.
	elided := "deja-2026…er-service"

	// show picks one, and says what to do with the other — without offering a
	// longer prefix, which does not exist here.
	out, err := captureRunStderr(t, "show", elided)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "prints them whole") {
		t.Errorf("show did not explain the ambiguity:\n%s", out)
	}
	if strings.Contains(out, "use a longer prefix") {
		t.Errorf("show still offers a prefix the reader cannot see:\n%s", out)
	}

	// The dry run changes nothing, so it explains the ambiguity instead of
	// erroring — that is the question someone runs it to answer.
	dry, err := captureRun(t, "forget", "--session", elided, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry, "matches 2 sessions") {
		t.Errorf("dry run did not say how many it matched:\n%s", dry)
	}

	// The real run refuses rather than dropping both.
	err = runForget(indexDirForTest(), []string{"--session", elided})
	if err == nil {
		t.Fatal("forget accepted an ambiguous elided id")
	}
	if !strings.Contains(err.Error(), "matches 2 sessions") {
		t.Errorf("forget did not say how many it matched: %v", err)
	}
	if got := index.Tombstones(); len(got) != 0 {
		t.Fatalf("the refusal still forgot: %v", got)
	}

	// An elided id that stands for exactly one session is not ambiguous and
	// must still work — refusing every elision would undo #855.
	if _, err := captureRun(t, "remember", "note about the davit ram seal", "--project", "payments-api"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--session", "deja-2026…ments-api", "--dry-run")
	if err != nil {
		t.Fatalf("an unambiguous elided id was refused: %v", err)
	}
	if !strings.Contains(out, "would drop: 1 session") {
		t.Errorf("an unambiguous elided id stopped matching:\n%s", out)
	}
}
