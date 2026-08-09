package stats

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestFilterNoCriteriaReturnsInput(t *testing.T) {
	ss := []model.Session{{Harness: "claude"}, {Harness: "codex"}}
	got := Filter(ss, search.Options{})
	if len(got) != 2 {
		t.Fatalf("no-criteria filter dropped sessions: %d", len(got))
	}
}

func TestFilterByHarnessAndProject(t *testing.T) {
	ss := []model.Session{
		{Harness: "claude", Project: "Agent-Fabric"},
		{Harness: "codex", Project: "agent-fabric"},
		{Harness: "claude", Project: "other"},
	}
	got := Filter(ss, search.Options{Harness: "claude"})
	if len(got) != 2 {
		t.Fatalf("harness filter = %d, want 2", len(got))
	}
	// Project match is case-insensitive substring.
	got = Filter(ss, search.Options{Project: "fabric"})
	if len(got) != 2 {
		t.Fatalf("project filter = %d, want 2", len(got))
	}
}

func TestFilterBySince(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{Harness: "a", Updated: now.Add(-time.Hour)},
		{Harness: "b", Updated: now.Add(-48 * time.Hour)},
	}
	got := Filter(ss, search.Options{Since: 24 * time.Hour})
	if len(got) != 1 || got[0].Harness != "a" {
		t.Fatalf("since filter = %+v, want only the recent one", got)
	}
}

func TestFilterByRoleKeepsOnlyMatchingMessages(t *testing.T) {
	ss := []model.Session{
		{Harness: "a", Messages: []model.Message{
			{Role: "user", Text: "q"},
			{Role: "assistant", Text: "a"},
			{Role: "user", Text: "q2"},
		}},
		{Harness: "b", Messages: []model.Message{
			{Role: "assistant", Text: "only assistant"},
		}},
	}
	got := Filter(ss, search.Options{Role: "user"})
	if len(got) != 1 {
		t.Fatalf("role filter kept %d sessions, want 1 (the one with user turns)", len(got))
	}
	if len(got[0].Messages) != 2 {
		t.Fatalf("role filter kept %d messages, want 2 user turns", len(got[0].Messages))
	}
	// The original session must not be mutated.
	if len(ss[0].Messages) != 3 {
		t.Fatalf("Filter mutated the caller's session: %d messages", len(ss[0].Messages))
	}
}

func TestScaledBar(t *testing.T) {
	cases := []struct{ n, maxN, width, want int }{
		{0, 10, 20, 0},   // no value, no bar
		{5, 0, 20, 0},    // no scale, guarded
		{10, 10, 20, 20}, // full
		{5, 10, 20, 10},  // half
		{1, 1000, 20, 1}, // tiny but non-zero rounds up to one cell
	}
	for _, c := range cases {
		if got := ScaledBar(c.n, c.maxN, c.width); got != c.want {
			t.Errorf("ScaledBar(%d,%d,%d) = %d, want %d", c.n, c.maxN, c.width, got, c.want)
		}
	}
}

func TestTrimRunes(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},    // fits
		{"exactly", 7, "exactly"}, // exact length
		{"truncate me", 5, "trun…"},
		{"x", 1, "x"},
		{"multibyte ✓ é ж", 5, "mult…"},
	}
	for _, c := range cases {
		if got := TrimRunes(c.s, c.n); got != c.want {
			t.Errorf("TrimRunes(%q,%d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
