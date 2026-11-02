package search

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWorkRecordInvariantAndWroteExclusion(t *testing.T) {
	for _, role := range []string{"tool-output", "files", "command", "edit", "summary", "wrote"} {
		if !IsWorkRecord(role) {
			t.Errorf("IsWorkRecord(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"user", "assistant", ""} {
		if IsWorkRecord(role) {
			t.Errorf("IsWorkRecord(%q) = true, want false", role)
		}
	}

	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	sess := model.Session{
		ID:      "claude:wrote-inv",
		Harness: "claude",
		Project: "deja-vu",
		Title:   "fix retry handler",
		Started: t0,
		Updated: t0.Add(2 * time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "update internal/retry/handler.go", Time: t0},
			{Role: "wrote", Text: "internal/retry/handler.go\n0123456789abcdef\nfedcba9876543210", Time: t0.Add(time.Minute)},
			{Role: "assistant", Text: "Updated the retry handler backoff.", Time: t0.Add(2 * time.Minute)},
		},
	}

	// 1. PrintContext must never treat "wrote" line-hash records as conversational speech,
	// even when queried for a term inside the "wrote" record or in overview mode.
	for _, q := range []string{"", "0123456789abcdef", "handler.go"} {
		var buf bytes.Buffer
		PrintContext(&buf, sess, q)
		out := buf.String()
		if strings.Contains(out, "0123456789abcdef") || strings.Contains(out, "## wrote") {
			t.Errorf("PrintContext(%q) leaked raw wrote blame hashes:\n%s", q, out)
		}
	}

	// 2. Direct search.Run (--no-index path) must never match raw "wrote" blame hashes.
	hits, err := Run([]model.Session{sess}, Options{Query: "0123456789abcdef"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("Run matched raw wrote hash: got %d hits", len(hits))
	}
}
