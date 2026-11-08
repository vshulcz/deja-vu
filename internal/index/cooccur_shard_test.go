package index

import (
	"fmt"
	"math"
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
	perSession := make([][]tokMask, 0, len(ss))
	for _, s := range ss {
		seen := map[string]int{}
		var list []tokMask
		nMsgs := len(s.Messages)
		for mIdx, m := range s.Messages {
			bit := turnWindowBit(mIdx, nMsgs)
			for _, tok := range tokens(m.Text) {
				if len(tok) < 4 || query.IsStopWord(tok) {
					continue
				}
				if idx, ok := seen[tok]; ok {
					list[idx].mask |= bit
					continue
				}
				seen[tok] = len(list)
				list = append(list, tokMask{tok: tok, mask: bit})
			}
		}
		for _, tm := range list {
			df[tm.tok]++
		}
		perSession = append(perSession, list)
	}
	maxDF := max(len(ss)/4, 8)
	type pair struct{ a, b string }
	type pairCounts struct{ c, turnC int }
	pairs := map[pair]pairCounts{}
	for _, list := range perSession {
		var kept []tokMask
		for _, tm := range list {
			if df[tm.tok] >= cooccurMinDF && df[tm.tok] <= maxDF {
				kept = append(kept, tm)
			}
		}
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i].tok] == df[kept[j].tok] {
				return kept[i].tok < kept[j].tok
			}
			return df[kept[i].tok] < df[kept[j].tok]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		for i := range kept {
			for j := i + 1; j < len(kept); j++ {
				a, b := kept[i], kept[j]
				if a.tok > b.tok {
					a, b = b, a
				}
				pc := pairs[pair{a.tok, b.tok}]
				pc.c++
				if (a.mask & (b.mask | (b.mask << 1) | (b.mask >> 1))) != 0 {
					pc.turnC++
				}
				pairs[pair{a.tok, b.tok}] = pc
			}
		}
	}
	type nc struct {
		t     string
		score float64
		c     int
	}
	cand := map[string][]nc{}
	totalDocs := float64(len(ss))
	for p, pc := range pairs {
		if pc.c < cooccurMinPair {
			continue
		}
		npmi := pairNPMI(totalDocs, df[p.a], df[p.b], pc.c)
		if len(ss) >= 10 && npmi <= cooccurMinNPMI {
			continue
		}
		turnRatio := float64(pc.turnC) / float64(pc.c)
		support := float64(pc.c) / float64(pc.c+cooccurMinPair)
		baseScore := (npmi + cooccurTurnWeight*turnRatio) * support
		cand[p.a] = append(cand[p.a], nc{t: p.b, score: baseScore * float64(pc.c) / float64(df[p.a]), c: pc.c})
		cand[p.b] = append(cand[p.b], nc{t: p.a, score: baseScore * float64(pc.c) / float64(df[p.b]), c: pc.c})
	}
	out := map[string][]string{}
	for tok, list := range cand {
		sort.Slice(list, func(i, j int) bool {
			if math.Abs(list[i].score-list[j].score) > 1e-9 {
				return list[i].score > list[j].score
			}
			if list[i].c != list[j].c {
				return list[i].c > list[j].c
			}
			return list[i].t < list[j].t
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
	for i := range sessions {
		var b strings.Builder
		x := uint32(i*2654435761 + 999) //nolint:gosec // wraps on purpose, it seeds a hash
		for range 40 {
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
