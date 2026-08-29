package main

import (
	"strings"
	"testing"
)

// Every line deja shows while a build runs promised "in a few seconds".
// Measured on this machine's 177 MB index, a full rebuild is 59 seconds — and
// the progress the same line carries says so itself: 3% of the sessions read is
// not a few seconds from done. The sentences an agent sees while waiting should
// not claim what deja can already see is false (#2598).
func TestTheBuildLinesDoNotPromiseSecondsTheyCannotKnow(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   warmupStatus
		want string
	}{
		{"just started", warmupStatus{Phase: "sessions", Done: 3, Total: 100}, "3%"},
		{"nearly done", warmupStatus{Phase: "sessions", Done: 97, Total: 100}, "97%"},
		{"no count yet", warmupStatus{Phase: "sessions"}, "indexing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.st.line()
			if !strings.Contains(line, tc.want) {
				t.Fatalf("line = %q, want it to carry %q", line, tc.want)
			}
			if strings.Contains(line, "few seconds") {
				t.Errorf("the line promises seconds it cannot know: %q", line)
			}
			// And it still says what it is doing and what the reader waits for.
			if !strings.Contains(line, "recall") {
				t.Errorf("the line stopped saying what the reader is waiting for: %q", line)
			}
		})
	}

	// The same claim lived on four surfaces; none of them may make it.
	t.Run("the other surfaces", func(t *testing.T) {
		hermeticEnv(t)
		for _, s := range []string{
			emptyRecallAnswerPolicy(t.TempDir(), "anything", 0),
			rememberSavedNote(t.TempDir()),
		} {
			if strings.Contains(s, "few seconds") {
				t.Errorf("still promising seconds: %q", s)
			}
		}
	})
}
