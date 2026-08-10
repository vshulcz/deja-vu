package index

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A corrupt or unreadable postings bucket on the relevance path used to be
// swallowed as "the term is absent", silently short-ranking the answer. It must
// surface as a corruption error so the recovery path rebuilds — the same
// self-heal the exact tier already triggers.
func TestRelevanceSurfacesACorruptBucket(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{
		{Harness: "claude", ID: "a", Project: "p", Messages: []model.Message{
			{Role: "user", Text: "how do we handle jwt refresh rotation reconciler"},
		}},
		{Harness: "claude", ID: "b", Project: "p", Messages: []model.Message{
			{Role: "user", Text: "the reconciler webhook retry cap needs raising"},
		}},
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Corrupt every bucket so any postings read fails with errCorruptIndex.
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no buckets to corrupt")
	}
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(dir, "buckets", e.Name()), []byte("XXXXXXXX garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A natural-language query that reaches the relevance tier.
	_, rerr := relevanceSearch(dir, m, query.Options{Query: "how do we handle the reconciler retry"})
	if rerr == nil {
		t.Fatal("a corrupt bucket was swallowed as an empty relevance result")
	}
	if !errors.Is(rerr, errCorruptIndex) {
		t.Errorf("relevance returned %v, want a corruption error the recovery path heals", rerr)
	}
}
