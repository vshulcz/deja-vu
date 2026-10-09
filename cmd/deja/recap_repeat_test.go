package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Three sessions of one project that reached the same conclusion printed it
// three times; the draft keeps it once, under the newest of them.
func TestRecapPrintsARepeatedConclusionOnce(t *testing.T) {
	now := time.Now()
	same := "Raised MaxConns to 20 so the burst stops queueing."
	r := index.Recap{Considered: 3, Spoke: 3, Projects: []string{"shop", "other"}, Sessions: []index.RecapSession{
		{Harness: "claude", ID: "aaaa1111", Project: "shop", When: now, Lines: []string{same, "Moved Postgres to a services block."}},
		{Harness: "claude", ID: "bbbb2222", Project: "shop", When: now.Add(-time.Hour), Lines: []string{same}},
		{Harness: "codex", ID: "cccc3333", Project: "other", When: now.Add(-2 * time.Hour), Lines: []string{same}},
	}}
	var out bytes.Buffer
	printRecap(&out, r, "7d", 0, "")
	s := out.String()
	if n := strings.Count(s, same); n != 2 {
		t.Errorf("conclusion printed %d times, want once per project:\n%s", n, s)
	}
	if strings.Contains(s, "bbbb2222") {
		t.Errorf("a session with nothing new still has a receipt:\n%s", s)
	}
}
