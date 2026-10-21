package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// nearCopyHits is indexedCollisionHits with one distinct reply appended to the
// full record of every session extend picks: the matched turns stay equal.
func nearCopyHits(t *testing.T, n int, extend func(i int) bool) (string, []search.Hit) {
	t.Helper()
	dir, hits := indexedCollisionHits(t, n, false)
	for i, h := range hits {
		if !extend(i) {
			continue
		}
		s := h.Session
		record, err := json.Marshal(map[string]any{
			"type": "user", "sessionId": s.ID, "uuid": "reply", "cwd": os.Getenv("HOME"),
			"timestamp": s.Updated.Add(time.Second).Format(time.RFC3339Nano),
			"message":   map[string]string{"role": "user", "content": fmt.Sprintf("distinct reply %d", i)},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", s.ID+".jsonl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(append(body, record...), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, hits
}

func countCopyOf(t *testing.T) *int {
	t.Helper()
	calls := 0
	t.Cleanup(func() { copyOf = search.CopyOf })
	copyOf = func(c, src model.Session) bool {
		calls++
		return search.CopyOf(c, src)
	}
	return &calls
}

// Sessions that match on the same turns and each go on with a reply of their
// own are siblings. Comparing every pair of their full records made 6000 of
// them take 4.7 s to search (#5523); a class is compared only with the classes
// whose last turn it holds.
func TestDropCopiedHitsNearCopiesCompareLinearly(t *testing.T) {
	const n = 400
	dir, hits := nearCopyHits(t, n, func(int) bool { return true })
	calls := countCopyOf(t)
	got := dropCopiedHits(dir, hits)
	if len(got) != n {
		t.Fatalf("siblings dropped: got %d hits, want %d", len(got), n)
	}
	t.Logf("%d near-copies, %d full-record comparisons", n, *calls)
	if *calls > n {
		t.Fatalf("%d near-copies made %d full-record comparisons, want at most %d", n, *calls, n)
	}
}

// Sessions that stop at the shared turns are sources of the ones that went on;
// the lookup has to find the same sources the pairwise scan found.
func TestCopySourcesNearCopiesMatchReference(t *testing.T) {
	const n = 60
	dir, hits := nearCopyHits(t, n, func(i int) bool { return i%4 != 1 })
	ss := make([]model.Session, n)
	for i, h := range hits {
		ss[i] = h.Session
	}
	for round := range 20 {
		maybe := func(src, c int) bool { return round == 0 || (src*7+c*11+round)%5 < 3 }
		want := referenceCopySources(dir, ss, maybe)
		got := copySources(dir, ss, func(src, c int, _ bool) bool { return maybe(src, c) })
		if round == 0 && len(want) != n-n/4 {
			t.Fatalf("reference found %d copies, want %d", len(want), n-n/4)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: got %v, want %v", round, got, want)
		}
	}
}
