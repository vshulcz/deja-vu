package search

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// This is the lookup the CLI falls back to while the index is unavailable, so
// an empty prefix has to be refused here too. Guarding only the index copy
// would make the behaviour depend on whether a rebuild happens to be running.
func TestEmptyPrefixFindsNothing(t *testing.T) {
	ss := []model.Session{
		{ID: "aa1", Harness: "claude", Project: "app"},
		{ID: "bb1", Harness: "codex", Project: "app"},
	}

	if s, ok := FindByPrefix(ss, ""); ok {
		t.Fatalf("an empty prefix returned session %q", s.ID)
	}
	if s, ok := FindByPrefix(ss, "bb"); !ok || s.ID != "bb1" {
		t.Fatalf("got %q ok=%v, want bb1 — the guard must not refuse real prefixes", s.ID, ok)
	}
	if _, ok := FindByPrefix(nil, ""); ok {
		t.Fatal("an empty store has nothing to match either")
	}
}
