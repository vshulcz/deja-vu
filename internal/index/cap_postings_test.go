package index

import (
	"testing"
)

func postingsFrom(sid uint32, n int, base int64) []posting {
	out := make([]posting, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, posting{Off: base + int64(i), Sid: sid})
	}
	return out
}

// TestCapPostingsKeepsEverySession is the property the cap must not break: it
// bounds how much of a session is read, never which sessions are candidates. A
// session dropped here would vanish from the results entirely.
func TestCapPostingsKeepsEverySession(t *testing.T) {
	var posts []posting
	posts = append(posts, postingsFrom(1, 500, 0)...)
	posts = append(posts, postingsFrom(2, 3, 1000)...)
	posts = append(posts, postingsFrom(3, 64, 2000)...)
	posts = append(posts, postingsFrom(4, 65, 3000)...)

	got := capPostingsPerSession(posts, 64)
	bySid := map[uint32]int{}
	for _, p := range got {
		bySid[p.Sid]++
	}
	for sid, want := range map[uint32]int{1: 64, 2: 3, 3: 64, 4: 64} {
		if bySid[sid] != want {
			t.Fatalf("sid %d kept %d, want %d", sid, bySid[sid], want)
		}
	}
	if len(bySid) != 4 {
		t.Fatalf("sessions kept %d, want 4", len(bySid))
	}
}

// TestCapPostingsSamplesAcrossTheSession checks the part that decides whether
// the cap loses conclusions: taking the first n would read only the opening of
// a long session, and what a session settled tends to be at its end.
func TestCapPostingsSamplesAcrossTheSession(t *testing.T) {
	posts := postingsFrom(7, 1000, 0)
	got := capPostingsPerSession(posts, 10)
	if len(got) != 10 {
		t.Fatalf("kept %d, want 10", len(got))
	}
	if got[len(got)-1].Off < 900 {
		t.Fatalf("last kept offset %d — the sample never reaches the end", got[len(got)-1].Off)
	}
	// Evenly spaced: no two kept offsets closer than a fraction of the stride.
	for i := 1; i < len(got); i++ {
		if d := got[i].Off - got[i-1].Off; d < 50 {
			t.Fatalf("kept offsets %d and %d are %d apart, not spread", got[i-1].Off, got[i].Off, d)
		}
	}
}

// TestCapPostingsUntouchedBelowTheBound guards the ordinary query: nothing is
// sampled, reordered, or copied when no session is over the bound.
func TestCapPostingsUntouchedBelowTheBound(t *testing.T) {
	posts := append(postingsFrom(1, 5, 0), postingsFrom(2, 9, 100)...)
	got := capPostingsPerSession(posts, 64)
	if len(got) != len(posts) {
		t.Fatalf("kept %d of %d", len(got), len(posts))
	}
	for i := range posts {
		if got[i] != posts[i] {
			t.Fatalf("posting %d changed", i)
		}
	}
	if n := capPostingsPerSession(posts, 0); len(n) != len(posts) {
		t.Fatalf("cap 0 kept %d, want all", len(n))
	}
}
