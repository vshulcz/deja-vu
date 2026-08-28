package index

import (
	"fmt"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The friction rules admit more lines than they did (#2430 through #2438), so
// what protects the answer is that a wall is counted by sessions rather than by
// occurrences. One session that fails four hundred times, and thirty ways once
// each, must not push out the error three separate sessions actually hit
// (#2446).
func TestOneNoisySessionDoesNotCrowdOutARealWall(t *testing.T) {
	now := time.Now()
	out := func(text string, at time.Time) model.Message {
		return model.Message{Role: roleToolOutput, Text: text, Time: at}
	}
	wall := "psql: error: connection to server at db-a, port 5432 failed: Connection refused"
	repeated := "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"
	// The runner's own prefix is stripped before a line is counted (#1637), so
	// the key a count lands under is the line as deja records it.
	repeatedKey, ok := FrictionLine(repeated)
	if !ok {
		t.Fatalf("the fixture is not friction at all: %s", repeated)
	}

	var sessions []model.Session
	for k := 0; k < 3; k++ {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("real%d", k), Harness: "claude", Project: "work/app",
			Updated:  now.Add(-time.Duration(k) * time.Hour),
			Messages: []model.Message{out(wall, now.Add(-time.Duration(k)*time.Hour))},
		})
	}
	noisy := model.Session{ID: "noisy", Harness: "claude", Project: "work/app", Updated: now}
	for i := 0; i < 400; i++ {
		noisy.Messages = append(noisy.Messages, out(repeated, now))
	}
	for i := 0; i < 30; i++ {
		noisy.Messages = append(noisy.Messages,
			out(fmt.Sprintf("handler_%d.go:11:2: undefined: repository.Missing%d in the orders package", i, i), now))
	}
	sessions = append(sessions, noisy)

	counts := map[string]int{}
	for _, s := range sessions {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			line, ok := FrictionLine(m.Text)
			if !ok || seen[line] {
				continue
			}
			seen[line] = true
			counts[line]++
		}
	}
	if got := counts[wall]; got != 3 {
		t.Errorf("the wall three sessions hit counts %d", got)
	}
	if got := counts[repeatedKey]; got != 1 {
		t.Errorf("four hundred failures in one session count %d, not once", got)
	}
	for line, n := range counts {
		if n >= FrictionMinSessions && line != wall {
			t.Errorf("a wall from one session reached the threshold: %q (%d)", line, n)
		}
	}
}
