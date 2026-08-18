package search

import (
	"strings"
	"testing"
)

// minSpanBrute is the answer tokenSpan has to give: the tightest window holding
// one occurrence of every token.
func minSpanBrute(low string, toks []string) int {
	places := make([][]int, len(toks))
	for i, tok := range toks {
		for at := 0; ; {
			j := strings.Index(low[at:], tok)
			if j < 0 {
				break
			}
			places[i] = append(places[i], at+j)
			at += j + 1
		}
		if len(places[i]) == 0 {
			return 0
		}
	}
	best := 1 << 30
	for _, p := range places[0] {
		lo, hi := p, p+len(toks[0])
		for i := 1; i < len(toks); i++ {
			bestGap, at := 1<<30, -1
			for _, q := range places[i] {
				gap := q - p
				if gap < 0 {
					gap = -gap
				}
				if gap < bestGap {
					bestGap, at = gap, q
				}
			}
			if at < lo {
				lo = at
			}
			if at+len(toks[i]) > hi {
				hi = at + len(toks[i])
			}
		}
		if hi-lo < best {
			best = hi - lo
		}
	}
	return best
}

// The case sampling could not see: every query word common, and the sentence
// where they meet sitting once, anywhere. Anchoring on the rarest token (#1318)
// answers it when one word is rare; when none is, the sample is chosen without
// knowing where the meeting is, and a 33 KB message measured 229 against a true
// 19 — across the boundary the proximity boost and the excerpt centring live on
// (#1319).
func TestTheTightestClusterIsFoundWhereverItSits(t *testing.T) {
	toks := []string{"retry", "queue", "staging"}
	for _, where := range []float64{0.03, 0.31, 0.5, 0.68, 0.97} {
		var b strings.Builder
		const n = 300
		at := int(float64(n) * where)
		for i := 0; i < n; i++ {
			switch {
			case i == at:
				b.WriteString("retry queue staging ")
			case i%3 == 0:
				b.WriteString("retry " + strings.Repeat("filler ", 15))
			case i%3 == 1:
				b.WriteString("queue " + strings.Repeat("filler ", 15))
			default:
				b.WriteString("staging " + strings.Repeat("filler ", 15))
			}
		}
		low := b.String()
		got := tokenWindow(low, toks)
		want := minSpanBrute(low, toks)
		if got != want {
			t.Errorf("meeting at %.0f%% of a %d-byte message: span %d, tightest is %d", where*100, len(low), got, want)
		}
		if got > proximityNear {
			t.Errorf("meeting at %.0f%%: span %d is past the %d boundary, so the hit loses its boost and its excerpt",
				where*100, got, proximityNear)
		}
	}
}

// And the answer is the tightest one on ordinary text too, wherever the real
// path runs. Where the first-occurrence bound already reads as one thought the
// cheap answer stands by design — it is an upper bound, it costs 101 ns against
// 122 µs for refining it, and both figures are inside the boost's flat end.
// Every message here is checked for which path it took.
func TestTheSpanMatchesTheBruteForceMinimum(t *testing.T) {
	words := []string{"retry", "queue", "staging", "jitter", "worker", "deploy"}
	toks := []string{"retry", "queue", "staging"}
	exact := 0
	for n := 0; n < 100; n++ {
		var b strings.Builder
		seed := n*7 + 3
		for i := 0; i < 600; i++ {
			if (i*seed)%23 == 0 {
				b.WriteString(words[(i*seed)%len(words)] + " ")
			} else {
				b.WriteString("filler ")
			}
		}
		low := b.String()
		got, want := tokenWindow(low, toks), minSpanBrute(low, toks)
		if want == 0 {
			continue
		}
		if cheapBound(low, toks) <= proximityNear {
			// The cheap path answered. It must still never claim the words sit
			// closer than they do.
			if got < want {
				t.Fatalf("message %d: the cheap bound %d is tighter than the true %d", n, got, want)
			}
			continue
		}
		exact++
		if got != want {
			t.Fatalf("message %d: span %d, tightest is %d", n, got, want)
		}
	}
	if exact == 0 {
		t.Fatal("no message reached the exact path, so this test proves nothing")
	}
}

// cheapBound is the first-occurrence span tokenSpan uses to decide whether
// refining is worth it.
func cheapBound(low string, toks []string) int {
	first, last := -1, -1
	for _, tok := range toks {
		i := strings.Index(low, tok)
		if i < 0 {
			return 0
		}
		if first < 0 || i < first {
			first = i
		}
		if end := i + len(tok); end > last {
			last = end
		}
	}
	return last - first
}

// A token the text does not carry still means no window, and a single-token
// query has none by definition.
func TestNoWindowWithoutEveryToken(t *testing.T) {
	if got := tokenWindow("the retry queue stalls", []string{"retry", "absent"}); got != 0 {
		t.Errorf("a missing token produced a window of %d", got)
	}
	if got := tokenWindow("the retry queue stalls", []string{"retry"}); got != 0 {
		t.Errorf("a single token produced a window of %d", got)
	}
}
