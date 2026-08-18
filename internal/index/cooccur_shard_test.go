package index

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// referenceCooccur is the neighbour map computed the obvious way: every pair of
// every session's kept tokens in one map, no sharding. Slow and memory-hungry by
// design — it exists to say what the answer is.
func referenceCooccur(ss []model.Session) map[string][]string {
	if len(ss) < cooccurMinDF || len(ss) > cooccurMaxSessions {
		return nil
	}
	df := map[string]int{}
	perSession := make([][]string, 0, len(ss))
	for _, s := range ss {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			for _, tok := range tokens(m.Text) {
				if len(tok) < 4 || query.IsStopWord(tok) || seen[tok] {
					continue
				}
				seen[tok] = true
			}
		}
		list := make([]string, 0, len(seen))
		for t := range seen {
			list = append(list, t)
			df[t]++
		}
		perSession = append(perSession, list)
	}
	maxDF := len(ss) / 4
	if maxDF < 8 {
		maxDF = 8
	}
	type pair struct{ a, b string }
	pairs := map[pair]int{}
	for _, list := range perSession {
		var kept []string
		for _, t := range list {
			if df[t] >= cooccurMinDF && df[t] <= maxDF {
				kept = append(kept, t)
			}
		}
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i]] == df[kept[j]] {
				return kept[i] < kept[j]
			}
			return df[kept[i]] < df[kept[j]]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		for i := 0; i < len(kept); i++ {
			for j := i + 1; j < len(kept); j++ {
				a, b := kept[i], kept[j]
				if a > b {
					a, b = b, a
				}
				pairs[pair{a, b}]++
			}
		}
	}
	type nc struct {
		t string
		c int
	}
	cand := map[string][]nc{}
	for p, c := range pairs {
		if c < cooccurMinPair {
			continue
		}
		cand[p.a] = append(cand[p.a], nc{p.b, c})
		cand[p.b] = append(cand[p.b], nc{p.a, c})
	}
	out := map[string][]string{}
	for tok, list := range cand {
		sort.Slice(list, func(i, j int) bool {
			if list[i].c == list[j].c {
				return list[i].t < list[j].t
			}
			return list[i].c > list[j].c
		})
		if len(list) > cooccurNeighbors {
			list = list[:cooccurNeighbors]
		}
		names := make([]string, len(list))
		for i, e := range list {
			names[i] = e.t
		}
		out[tok] = names
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shardTestCorpus draws each session's tokens from a vocabulary far larger than
// any one session, so pairs land on both sides of cooccurMinPair rather than all
// surviving or all being dropped. Measured on 300 sessions: 13903 pairs seen
// once, 19302 twice (both dropped), 18074 exactly at the threshold of three, and
// 23475 above it.
func shardTestCorpus(sessions int) []model.Session {
	ss := make([]model.Session, 0, sessions)
	for i := 0; i < sessions; i++ {
		var b strings.Builder
		x := uint32(i*2654435761 + 999)
		for j := 0; j < 40; j++ {
			x = x*1664525 + 1013904223
			fmt.Fprintf(&b, "token%05d ", x%400)
		}
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%04d", i), Project: "app",
			Messages: []model.Message{{Role: "user", Text: b.String()}},
		})
	}
	return ss
}

// Counting the pairs a shard at a time is an accounting change, not a change of
// answer: each pair lands in exactly one shard, and the neighbour lists are
// sorted by count then token, so which shard was walked first cannot show
// through (#1137).
func TestShardedCooccurMatchesTheObviousComputation(t *testing.T) {
	ss := shardTestCorpus(300)
	want := referenceCooccur(ss)
	if len(want) == 0 {
		t.Fatal("the corpus produced no neighbours, so the test compares nothing")
	}
	dir := t.TempDir()
	buildCooccur(dir, ss)
	got := readCooccur(dir)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sharded build differs from the obvious one: %d tokens vs %d", len(got), len(want))
		for tok, list := range want {
			if !reflect.DeepEqual(got[tok], list) {
				t.Errorf("  %q: got %v, want %v", tok, got[tok], list)
				break
			}
		}
	}
}

// Every shard count must give the same answer, which is what makes the constant
// free to tune.
func TestCooccurAnswerDoesNotDependOnShardCount(t *testing.T) {
	if cooccurShards < 2 {
		t.Fatal("sharding is off, so this proves nothing")
	}
	ss := shardTestCorpus(300)
	dir := t.TempDir()
	buildCooccur(dir, ss)
	got := readCooccur(dir)
	if want := referenceCooccur(ss); !reflect.DeepEqual(got, want) {
		t.Errorf("shard count %d changes the answer", cooccurShards)
	}
}
