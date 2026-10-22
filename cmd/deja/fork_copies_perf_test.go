package main

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func collisionHits(n int, unique bool) []search.Hit {
	hits := make([]search.Hit, n)
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for i := range hits {
		s := model.Session{ID: strconv.Itoa(i), Harness: "codex", Started: at}
		for j := range 12 {
			text := fmt.Sprintf("WARMUP_SENTINEL native synthetic %d", j)
			if unique {
				text += strconv.Itoa(i)
			}
			s.Messages = append(s.Messages, model.Message{Role: "user", Time: at, Text: text})
		}
		hits[i].Session = s
	}
	return hits
}

func TestDropCopiedHitsCollisionAllocations(t *testing.T) {
	hits := collisionHits(100, false)
	allocs := testing.AllocsPerRun(1, func() {
		if got := dropCopiedHits("", hits); len(got) != len(hits) {
			t.Fatalf("identical peers lost: got %d, want %d", len(got), len(hits))
		}
	})
	if allocs > 10000 {
		t.Fatalf("100 colliding sessions allocated %.0f objects; turn keys must be reused across pairs", allocs)
	}
}

func TestDropCopiedHitsRetainsForkAttribution(t *testing.T) {
	hits := collisionHits(3, false)
	hits[1].Session.Kind, hits[1].Session.Parent = "fork", hits[0].Session.ID
	hits[2].Session.Kind, hits[2].Session.Parent = "fork", hits[1].Session.ID
	if got := dropCopiedHits("", hits); !reflect.DeepEqual(got, hits[:1]) {
		t.Fatalf("copied fork lineage changed: got %#v", got)
	}
	hits[1].Session.Messages = append(hits[1].Session.Messages, model.Message{
		Role: "user", Time: hits[1].Session.Started.Add(time.Second), Text: "fork owns this turn",
	})
	if got := dropCopiedHits("", hits); !reflect.DeepEqual(got, hits[:2]) {
		t.Fatalf("fork's own matching turn lost: got %#v", got)
	}
}

func BenchmarkDropCopiedHitsCollisions(b *testing.B) {
	for _, unique := range []bool{false, true} {
		for _, n := range []int{5, 20, 100, 800} {
			b.Run(fmt.Sprintf("unique=%v/n=%d", unique, n), func(b *testing.B) {
				hits := collisionHits(n, unique)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if got := dropCopiedHits("", hits); len(got) != n {
						b.Fatalf("got %d peers, want %d", len(got), n)
					}
				}
			})
		}
	}
}
