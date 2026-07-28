package index

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// EnsureForSearchStale decides whether a hook can answer from the index it has
// while a rebuild happens behind it. Getting it wrong either blocks a session
// start on a full rebuild or serves an index that is missing the answer.
func TestEnsureForSearchStaleDecidesWhatToServe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	path := filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"))

	// No index yet: nothing sensible to serve stale, so it must build.
	stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("an absent index was reported as servable")
	}
	if !HasManifest(dir) {
		t.Fatal("no index was built")
	}

	// Nothing changed: no work, nothing stale.
	if stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || stale {
		t.Fatalf("unchanged store: stale=%v err=%v", stale, err)
	}

	// A plain append is cheap enough to absorb synchronously.
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "first"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	if stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || stale {
		t.Fatalf("append should be absorbed in place: stale=%v err=%v", stale, err)
	}

	// A removed file cannot be absorbed by appending: the caller has to
	// rebuild, and until it does the current snapshot is what gets served.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	stale, err = EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("a removed transcript did not ask for a rebuild")
	}
}

// A rewound file must not take the append path here either, or the hook
// serves an index holding text the transcript no longer has.
func TestEnsureForSearchStaleRefusesToAppendOntoARewind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	path := filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "ORIGINAL"), claudeLine("s1", "2026-01-01T00:02:00Z", "second"))
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeLines(t, path,
		claudeLine("s1", "2026-01-01T00:01:00Z", "REWRITTEN"),
		claudeLine("s1", "2026-01-01T00:02:00Z", "second"),
		claudeLine("s1", "2026-01-01T00:03:00Z", "third"))
	stale, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("a rewritten prefix was absorbed as an append; the old text would stay indexed")
	}
}

// RecentProjects is how the session-start digest picks what to show: the most
// recent sessions of the projects the working tree points at. Matching is by
// substring because project names arrive in different shapes per harness, and
// a session must never be listed twice when two names both match it.
func TestRecentProjectsRanksAndDeduplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	for _, tc := range []struct{ project, id, ts, text string }{
		{"alpha", "a1", "2026-01-01T00:01:00Z", "oldest alpha"},
		{"alpha", "a2", "2026-03-01T00:01:00Z", "newest alpha"},
		{"beta", "b1", "2026-02-01T00:01:00Z", "only beta"},
	} {
		writeLines(t, filepath.Join(claude, tc.project, tc.id+".jsonl"), claudeLine(tc.id, tc.ts, tc.text))
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}

	// Newest first, and the per-name cap applies to each name separately.
	got, err := RecentProjects(dir, []string{"alpha"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("got %+v, want only the newest alpha session", ids(got))
	}

	// Two names that both match the same session must not list it twice —
	// the digest would spend its budget saying the same thing.
	both, err := RecentProjects(dir, []string{"alpha", "alph"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range both {
		if seen[s.ID] {
			t.Fatalf("session %s listed twice: %v", s.ID, ids(both))
		}
		seen[s.ID] = true
	}
	if len(both) != 2 {
		t.Fatalf("got %v, want both alpha sessions once each", ids(both))
	}

	// An unknown project yields nothing rather than everything, or a fresh
	// checkout would recall the whole machine.
	if got, err := RecentProjects(dir, []string{"gamma"}, 5); err != nil || len(got) != 0 {
		t.Fatalf("unknown project returned %v (%v)", ids(got), err)
	}
}

func ids(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
