package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The MCP blame tool used to marshal whole hits, which carry the session's
// entire message list: one call on a common path returned 495 KB into an
// agent's context, against the ~4 KB the other tools answer in.
func TestMCPBlameLeavesTheTranscriptBehind(t *testing.T) {
	var msgs []model.Message
	for i := 0; i < 200; i++ {
		msgs = append(msgs, model.Message{Role: "assistant", Text: strings.Repeat("x", 500)})
	}
	hits := []search.BlameHit{{
		Session: model.Session{
			ID: "s1", Harness: "claude", Project: "api", Title: "pool exhaustion",
			Updated: time.Now(), Messages: msgs, Touched: []string{"/w/pool.go"},
		},
		Title: "pool exhaustion", Count: 3, Score: 1.5, Tier: "exact",
		Snippets: []string{"we chose transaction pooling"},
	}}
	out := renderMCPBlame("pool.go", "pool.go", hits, 0, "")
	if len(out) > 4096 {
		t.Fatalf("blame answered %d bytes for one hit; the transcript is still in there", len(out))
	}
	if strings.Contains(out, strings.Repeat("x", 100)) {
		t.Fatal("the message list must not travel to an agent")
	}
	// Everything an agent reads has to survive: who, where, which session,
	// what it was about and what it said.
	for _, want := range []string{"[claude] api · s1 · 3 mentions", "title: pool exhaustion", "- we chose transaction pooling", "untrusted reference data"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lost %q:\n%s", want, out)
		}
	}
}

// blame is asked "who decided this", and the JSON it answered in had no field
// for a decision taken back: attachBlameLifecycles set it and the agent never
// saw it (#4634).
func TestMCPBlameSaysTheDecisionWasTakenBack(t *testing.T) {
	hits := []search.BlameHit{{
		Session:   model.Session{ID: "s1", Harness: "claude", Project: "api", Title: "pool size"},
		Count:     2,
		Snippets:  []string{"set the pool to 50"},
		Lifecycle: "rejected", LifecycleAt: "2026-09-01", LifecycleNote: "50 starved the replicas",
	}}
	out := renderMCPBlame("pool.go", "pool.go", hits, 0, "")
	for _, want := range []string{"[this was tried and rejected, 2026-09-01]", "50 starved the replicas"} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lost %q:\n%s", want, out)
		}
	}
}
