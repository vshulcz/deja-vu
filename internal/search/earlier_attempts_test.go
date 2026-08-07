package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// EarlierAttempts marks a session that a newer session in the same project
// contradicts, so the injected block can say which one the project settled on.
func TestEarlierAttempts(t *testing.T) {
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	older := model.Session{
		ID: "old", Harness: "claude", Project: "p", Updated: base,
		Messages: []model.Message{msg("switch the pool to transaction mode for pgbouncer latency")},
	}
	newer := model.Session{
		ID: "new", Harness: "claude", Project: "p", Updated: base.Add(72 * time.Hour),
		Messages: []model.Message{msg("switch the pool back — transaction mode broke prepared statements pgbouncer")},
	}
	got := EarlierAttempts([]model.Session{older, newer})
	if got["old"] != newer.Updated.Format("2006-01-02") {
		t.Errorf("older session not marked with the newer date: %v", got)
	}
	if got["new"] != "" {
		t.Errorf("the newest session was marked as an earlier attempt: %v", got)
	}

	// Different project — not the same ground, no mark.
	other := newer
	other.Project = "q"
	if m := EarlierAttempts([]model.Session{older, other}); m["old"] != "" {
		t.Errorf("a session in a different project was treated as a contradiction: %v", m)
	}

	// Less than a day apart — that is iteration, not a settled contradiction.
	soon := newer
	soon.Updated = base.Add(2 * time.Hour)
	if m := EarlierAttempts([]model.Session{older, soon}); m["old"] != "" {
		t.Errorf("two sessions hours apart were called contradictory: %v", m)
	}

	// Low word overlap — same project, unrelated work.
	unrelated := model.Session{
		ID: "u2", Harness: "claude", Project: "p", Updated: base.Add(72 * time.Hour),
		Messages: []model.Message{msg("write the release notes and update the changelog for shipping")},
	}
	if m := EarlierAttempts([]model.Session{older, unrelated}); m["old"] != "" {
		t.Errorf("unrelated same-project work was called a contradiction: %v", m)
	}
}
