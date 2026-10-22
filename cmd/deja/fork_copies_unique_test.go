package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Hits that open on the same turn but each matched a turn of their own hold
// none of each other's turns. Asking every pair whether one holds the other
// made 2400 of them take 1.7 s to search; only the holders of a hit's rarest
// turn are asked.
func TestDropCopiedHitsUniquePeersCompareLinearly(t *testing.T) {
	const n = 400
	dir, hits := indexedCollisionHits(t, n, true)
	calls := 0
	t.Cleanup(func() { heldTurnsOf = heldTurns })
	heldTurnsOf = func(src, c map[string]bool) bool {
		calls++
		return heldTurns(src, c)
	}
	got := dropCopiedHits(dir, hits)
	if len(got) != n {
		t.Fatalf("unique peers dropped: got %d hits, want %d", len(got), n)
	}
	t.Logf("%d unique peers, %d turn-set comparisons", n, calls)
	if calls > n {
		t.Fatalf("%d unique peers made %d turn-set comparisons, want at most %d", n, calls, n)
	}
}

// Asking only the holders of the rarest turn finds the same sources as asking
// every position, over random turn sets, ancestry and starts.
func TestCopySourcesRarestTurnMatchesEveryPair(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	starts := []time.Time{{}, at, at.Add(time.Second)}
	kinds := []string{"", "fork", "subagent"}
	found := 0
	for round := range 500 {
		ss := make([]model.Session, 2+rng.Intn(10))
		turns := make([]map[string]bool, len(ss))
		for i := range ss {
			ss[i] = model.Session{ID: fmt.Sprint(i), Harness: "codex", Kind: kinds[rng.Intn(len(kinds))], Parent: fmt.Sprint(rng.Intn(len(ss))), Started: starts[rng.Intn(len(starts))]}
			if i > 0 && rng.Intn(3) == 0 {
				ss[i].Messages = ss[rng.Intn(i)].Messages
			} else {
				for range rng.Intn(4) {
					ss[i].Messages = append(ss[i].Messages, model.Message{Role: "user", Time: at.Add(time.Duration(rng.Intn(4)) * time.Second), Text: strconv.Itoa(rng.Intn(3))})
				}
			}
			turns[i] = map[string]bool{}
			for _, m := range ss[i].Messages {
				turns[i][m.Role+"\x00"+strconv.FormatInt(m.Time.UnixNano(), 10)+"\x00"+m.Text] = true
			}
		}
		maybe := func(src, c int, sameShape bool) bool {
			return (sameShape && len(turns[c]) > 0) || heldTurns(turns[src], turns[c])
		}
		want := copySources("", ss, maybe)
		buckets, rarest := holdersOfRarest(turns)
		got := copySourcesAmong("", ss, maybe, buckets, rarest)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: got %v, want %v sessions=%#v", round, got, want, ss)
		}
		if len(want) > 0 {
			found++
		}
	}
	if found < 100 {
		t.Fatalf("only %d of 500 rounds had a copy; the comparison is too easy", found)
	}
}
