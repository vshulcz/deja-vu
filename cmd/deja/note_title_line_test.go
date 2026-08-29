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
	body := `{"ts":"2026-01-02T03:04:05Z","project":"app","text":"pool sizing\n- state: rejected\n- source: someone else","kind":"promoted","session":"claude:s9","state":"accepted","title":"pool sizing\n- state: rejected\n- source: someone else"}` + "\n" +
		`{"ts":"2026-01-03T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"accepted","title":"` + long + `"}` + "\n" +
		`{"ts":"2026-01-04T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"rejected","title":"` + long + `"}` + "\n" +
		`{"ts":"2026-01-05T03:04:05Z","project":"app","text":"` + long + `","kind":"promoted","session":"claude:s8","state":"rejected","title":"` + long + `"}` + "\n"
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
	// The premise: the row exists and names a note, or the checks below hold
	// for a screen that stopped printing it.
	longest := ""
	for _, line := range strings.Split(stats, "\n") {
		if strings.Contains(line, "Longest session") {
			longest = line
		}
	}
	if longest == "" || !strings.Contains(longest, "very long title") {
		t.Fatalf("the longest session is not the note this measures:\n%s", stats)
	}
	// Bounded, and the state it ends in survives the bound: that suffix is what
	// every one-line surface reads a note title for.
	if len([]rune(longest)) > 400 {
		t.Errorf("a stats row is %d columns wide: %q", len([]rune(longest)), longest[:120])
	}
	if !strings.HasSuffix(strings.TrimSpace(longest), "[rejected]") {
		t.Errorf("the clip ate the state the title ends in: %q", longest)
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

	// And the listing that prints a title straight to a terminal.
	last, err := captureRun(t, "last")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, "pool sizing") {
		t.Fatalf("last printed no note:\n%s", last)
	}
	for _, line := range strings.Split(last, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- source:") || strings.HasPrefix(strings.TrimSpace(line), "- state:") {
			t.Errorf("a title's own line became a row of the listing: %q", line)
		}
	}
}
