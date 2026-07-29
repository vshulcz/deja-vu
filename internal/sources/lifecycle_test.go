package sources

import (
	"os"
	"path/filepath"
	"testing"
)

func writeNotes(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A session's state is whatever was recorded last. Getting this backwards means
// a correction is ignored and the thing it corrected is served as current — the
// exact failure this map exists to prevent.
func TestLatestRecordedStateWins(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"accepted","text":"looked right","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"reverted a week later","ts":"2026-01-08T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s2","state":"accepted","text":"still holds","ts":"2026-01-02T00:00:00Z"}`,
	)
	got := promotedLifecyclesFrom(path)
	if len(got) != 2 {
		t.Fatalf("sessions = %v", LifecycleKeys(got))
	}
	if got["claude:s1"].State != "rejected" || got["claude:s1"].Note != "reverted a week later" {
		t.Fatalf("s1 = %+v, want the later rejection", got["claude:s1"])
	}
	if got["claude:s2"].State != "accepted" {
		t.Fatalf("s2 = %+v", got["claude:s2"])
	}
}

// Two entries stamped in the same second still have an order: the file's. A
// correction written moments after the note it corrects is still the correction.
func TestSameTimestampFallsBackToFileOrder(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"accepted","text":"first","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"superseded","text":"second","ts":"2026-01-01T00:00:00Z"}`,
	)
	if got := promotedLifecyclesFrom(path)["claude:s1"]; got.State != "superseded" || got.Note != "second" {
		t.Fatalf("got %+v, want the later line", got)
	}
}

// Everything that is not a promotion with a known state is ignored rather than
// guessed at: a plain note, an unknown state, a line with no session, and text
// that is not JSON at all.
func TestOnlyPromotionsWithKnownStatesCount(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"note","project":"p","text":"a plain note","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"invented","text":"nope","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"","state":"rejected","text":"no session","ts":"2026-01-01T00:00:00Z"}`,
		`not json at all`,
		``,
		`{"kind":"promoted","session":"codex:s9","state":"stale","text":"kept","ts":"2026-01-01T00:00:00Z"}`,
	)
	got := promotedLifecyclesFrom(path)
	if len(got) != 1 || got["codex:s9"].State != "stale" {
		t.Fatalf("got %v", LifecycleKeys(got))
	}
}

func TestMissingNotesFileIsNotAnError(t *testing.T) {
	if got := promotedLifecyclesFrom(filepath.Join(t.TempDir(), "absent.jsonl")); got != nil {
		t.Fatalf("got %v, want nothing", LifecycleKeys(got))
	}
	if got := promotedLifecyclesFrom(writeNotes(t)); got != nil {
		t.Fatalf("an empty file produced %v", LifecycleKeys(got))
	}
}

// A note with no timestamp still records a state; it just cannot claim to be
// newer than one that has a date.
func TestUndatedEntriesStillCount(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"undated"}`,
	)
	got := promotedLifecyclesFrom(path)["claude:s1"]
	if got.State != "rejected" || !got.At.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestPromotedLifecyclesReadsTheConfiguredFile(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"why","ts":"2026-01-01T00:00:00Z"}`,
	)
	t.Setenv("DEJA_NOTES_FILE", path)
	if got := PromotedLifecycles()["claude:s1"]; got.State != "rejected" {
		t.Fatalf("got %+v", got)
	}
}
