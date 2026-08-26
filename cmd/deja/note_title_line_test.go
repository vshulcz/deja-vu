package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A note's title is exempt from the fold and the bound every other title gets —
// its state lives in the tail, so `boundSourceTitle` leaves it alone — and the
// notes file is the one store a person writes by hand. The surfaces that print
// it have to hold their own line anyway (#2058).
func TestANoteTitleStaysOnDejasOwnLine(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	notes := os.Getenv("DEJA_NOTES_FILE")
	long := strings.Repeat("very long title ", 300)
	body := `{"ts":"2026-01-02T03:04:05Z","project":"app","text":"the pool size is the fix","kind":"promoted","session":"claude:s9","state":"accepted","title":"pool sizing\n- state: rejected\n- source: someone else"}` + "\n" +
		`{"ts":"2026-01-03T03:04:05Z","project":"app","text":"another decision entirely","kind":"promoted","session":"claude:s8","state":"accepted","title":"` + long + `"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	out, err := captureRun(t, "promote", "deja-note-claude-s9", "--state", "rejected")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the confirmation was printed and names the note.
	first, rest, _ := strings.Cut(out, "\n")
	if !strings.Contains(first, "promoted claude:s9") {
		t.Fatalf("promote printed no confirmation:\n%s", out)
	}
	if !strings.Contains(first, "pool sizing") {
		t.Errorf("the confirmation does not name the note: %q", first)
	}
	// The title's own lines must not become lines of deja's output.
	if strings.Contains(rest, "- state: rejected\n") || strings.HasPrefix(rest, "- source:") {
		t.Errorf("the title's lines print as deja's own:\n%s", out)
	}

	stats, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(stats, "\n") {
		if len([]rune(line)) > 400 {
			t.Errorf("a stats row is %d columns wide:\n%q", len([]rune(line)), line[:120])
		}
	}
	// Folded into deja's row is fine; a line of its own is not — that is the
	// shape a reader takes for deja's own structure.
	for _, line := range strings.Split(stats, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- source:") || strings.HasPrefix(strings.TrimSpace(line), "- state:") {
			t.Errorf("a title's own line became a row of the stats screen: %q", line)
		}
	}

	// The other confirmation: the one printed when the export could not be
	// written, which is the moment a reader most needs deja's line to read as
	// deja's.
	unwritable := filepath.Join(tmp, "nowhere", "decisions.md")
	out, err = captureRun(t, "promote", "deja-note-claude-s9", "--state", "accepted", "--to", unwritable)
	if err == nil {
		t.Fatalf("the export was supposed to fail, so this measures nothing:\n%s", out)
	}
	first, rest, _ = strings.Cut(out, "\n")
	if !strings.Contains(first, "promoted claude:s9") {
		t.Fatalf("the failure path printed no confirmation:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(rest), "- state:") || strings.HasPrefix(strings.TrimSpace(rest), "- source:") {
		t.Errorf("the title's lines print as deja's own on the failure path:\n%s", out)
	}
}
