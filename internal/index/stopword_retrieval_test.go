package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestStopWordsDoNotConstrainRetrieval(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	// The record does NOT contain the stop word "before".
	sessions := []model.Session{{ID: "s", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "fix the race condition on commit"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Natural-language query with a stop word ("before") that is NOT in the
	// record. The content tokens race+commit must still find it.
	hits, err := Search(dir, search.Options{Query: "race before commit", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("stop word over-constrained retrieval: got %d hits", len(hits))
	}
}
