package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The note outlives the session it came from — that is what forget promises
// when it keeps it. But the id promote hands out for corrections then resolves
// to the note, both routes refused, and the advice named a session that is gone
// for good: the note froze at `accepted` and recall kept serving a withdrawn
// decision (#979).
func TestCorrectionReachesANoteWhoseSourceIsForgotten(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"should we raise the pool cap"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"srcx","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "srcx", "--state", "accepted", "--note", "pool cap goes to 200"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "srcx"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "promote", "srcx", "--state", "rejected", "--note", "rolled back, the cap stays at 20")
	if err != nil {
		t.Fatalf("the correction was refused: %v", err)
	}
	if !strings.Contains(out, "as rejected") {
		t.Errorf("the correction did not land:\n%s", out)
	}
	// It belongs to the note the decision lives in, not to a second one, and
	// it leads — the withdrawal is the answer that holds.
	shown, err := captureRun(t, "show", "deja-note-claude-srcx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "rolled back") || !strings.Contains(shown, "pool cap goes to 200") {
		t.Errorf("the note lost one of the two marks:\n%s", shown)
	}
	if strings.Index(shown, "rolled back") > strings.Index(shown, "pool cap goes to 200") {
		t.Errorf("the correction sits behind the mark it takes back:\n%s", shown)
	}
	// The title of a note whose borrowed one forget cleared must not be the
	// display fallback echoed back at the reader.
	if strings.Contains(out, "promoted from claude:srcx") {
		t.Errorf("the correction took the note's display line as its title:\n%s", out)
	}

	// The backside: a note someone wrote with `remember` is not a promotion,
	// and turning it into one would invent a source session.
	if _, err := captureRun(t, "remember", "plain note about the cert"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	day := ""
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "deja-20") {
			day = m.ID
		}
	}
	if day == "" {
		t.Fatal("no day note to try")
	}
	if _, err := captureRun(t, "promote", day, "--state", "rejected"); err == nil {
		t.Errorf("a note written by hand was promoted as if it had a source session")
	} else if !strings.Contains(err.Error(), "not a session") {
		t.Errorf("the refusal does not say what the id is: %v", err)
	}
}
