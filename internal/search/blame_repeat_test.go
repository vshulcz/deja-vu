package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A fork, a resumed session and a subagent run each carry the transcript they
// came from, so one piece of evidence arrives under several ids. Read from a
// real store, `deja blame internal/search/search.go` quoted the same two lines
// under four sessions — all four forks of one — and those eight lines were most
// of the answer. On that file the quoted lines went from 18 to 11.
func TestBlameSaysAPieceOfEvidenceOnce(t *testing.T) {
	now := time.Now().UTC()
	shared := "internal/search/search.go carries the lifecycle field and the summary printed under it"
	own := "internal/search/search.go got the freshness floor raised to keep a year-old session usable"
	session := func(id, extra string, ago time.Duration) model.Session {
		msgs := []model.Message{{Role: "assistant", Text: shared, Time: now.Add(-ago)}}
		if extra != "" {
			msgs = append(msgs, model.Message{Role: "assistant", Text: extra, Time: now.Add(-ago)})
		}
		return model.Session{Harness: "claude", ID: id, Project: "deja-vu",
			Updated: now.Add(-ago), Messages: msgs}
	}
	ss := []model.Session{
		session("fork-a", own, time.Hour),
		session("fork-b", "", 2*time.Hour),
		session("fork-c", "", 3*time.Hour),
	}
	hits := Blame(ss, BlameTarget{Base: "search.go", FullPath: "internal/search/search.go"}, BlameOptions{})
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want every session that touched the file", len(hits))
	}
	seen := 0
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "carries the lifecycle field") {
				seen++
			}
		}
	}
	if seen != 1 {
		t.Errorf("the same evidence is quoted %d times", seen)
	}
	// The session that had something of its own still says it.
	var kept bool
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "freshness floor") {
				kept = true
			}
		}
	}
	if !kept {
		t.Error("a session's own evidence was dropped with the duplicate")
	}
}
