package sources

import (
	"slices"
	"testing"
	"time"
)

func TestNoteFileRefsAndStaleCandidateNotes(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	note := PromotedNote{
		Project: "deja-vu",
		Session: "claude:s1",
		State:   "accepted",
		Title:   "Keep retry cap in internal/index/ingest.go:42",
		Text:    "Also see `cmd/deja/promote.go` and https://example.com/foo.go for context.",
		At:      t0,
	}

	refs := NoteFileRefs(note)
	for _, want := range []string{"internal/index/ingest.go", "cmd/deja/promote.go"} {
		if !slices.Contains(refs, want) {
			t.Fatalf("NoteFileRefs = %v, missing %q", refs, want)
		}
	}
	for _, r := range refs {
		if r == "https://example.com/foo.go" {
			t.Fatalf("NoteFileRefs should ignore URLs, got %v", refs)
		}
	}

	stale := StaleCandidateNotes([]PromotedNote{note}, map[string]time.Time{
		"/work/deja-vu/internal/index/ingest.go": t0.Add(2 * time.Hour),
	})
	if len(stale) != 1 || stale[0].File != "internal/index/ingest.go" {
		t.Fatalf("StaleCandidateNotes = %+v, want 1 entry for internal/index/ingest.go", stale)
	}

	notStale := StaleCandidateNotes([]PromotedNote{note}, map[string]time.Time{
		"/work/deja-vu/internal/index/ingest.go": t0.Add(-time.Hour),
	})
	if len(notStale) != 0 {
		t.Fatalf("StaleCandidateNotes with older touch = %+v, want empty", notStale)
	}
}

func TestConflictingNotesDetectsSharedFileRef(t *testing.T) {
	a := PromotedNote{
		Project: "deja-vu",
		Session: "claude:a",
		State:   "accepted",
		Title:   "Pool cap in pool.go",
		Text:    "Max 4 idle conns.",
	}
	b := PromotedNote{
		Project: "deja-vu",
		Session: "claude:b",
		State:   "accepted",
		Title:   "Raise limit in internal/db/pool.go",
		Text:    "Set to 16.",
	}
	got := ConflictingNotes(b, []PromotedNote{a, b})
	if len(got) != 1 || got[0].Session != "claude:a" {
		t.Fatalf("ConflictingNotes = %+v, want claude:a via shared pool.go", got)
	}
}
