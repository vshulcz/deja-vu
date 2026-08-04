package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Forget counts the user's own writing apart from transcripts, and the two
// shapes deja files under its own harness are not the same thing: a day bucket
// comes from `remember`, a deja-note- session from `promote`. Calling a day of
// notes a promoted note names something the reader may never have done (#957).
func TestForgetCountsPromotedNotesApartFromDayBuckets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","timestamp":"2026-07-11T10:00:00Z","cwd":"/p","message":{"role":"user","content":"a session about the ticker"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"ts":"2026-07-12T09:00:00Z","project":"p","text":"a note of my own"}` + "\n" +
		`{"ts":"2026-07-12T10:00:00Z","project":"p","text":"the decision that held","kind":"promoted","session":"claude:s1","state":"accepted","title":"decision"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got, err := Forget(dir, ForgetOptions{Project: "p", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != 2 {
		t.Fatalf("counted %d of the user's own sessions, want 2 (a day bucket and a promoted note)", got.Notes)
	}
	if got.Promoted != 1 {
		t.Errorf("counted %d promoted notes, want 1 — the day bucket is not one", got.Promoted)
	}
}

// The undo has to be able to refuse the way forget does, so the count and the
// action share one selector (#961).
func TestTombstoneMatchesCountsWhatUnforgetWouldLift(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"ab-one", "ab-two", "zz-other"} {
		rec := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-07-1` + string(rune('1'+i)) + `T10:00:00Z","cwd":"/p","message":{"role":"user","content":"session ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "ab"}); err != nil {
		t.Fatal(err)
	}

	if n := TombstoneMatches("ab"); n != 2 {
		t.Errorf("TombstoneMatches(\"ab\") = %d, want 2", n)
	}
	if n := TombstoneMatches("claude:"); n != 2 {
		t.Errorf("a harness-scoped prefix counted %d, want 2", n)
	}
	if n := TombstoneMatches("claude:ab-one"); n != 1 {
		t.Errorf("one exact key counted %d, want 1", n)
	}
	if n := TombstoneMatches("nothing-like-this"); n != 0 {
		t.Errorf("a selector matching nothing counted %d", n)
	}
	// And what it counted is what Unforget lifts.
	lifted, err := Unforget(dir, "ab", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lifted != 2 {
		t.Errorf("Unforget lifted %d where the count said 2", lifted)
	}
}
