package main

import (
	"encoding/json"
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
	out := mustMarshalBlame(hits)
	if len(out) > 4096 {
		t.Fatalf("blame answered %d bytes for one hit; the transcript is still in there", len(out))
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	sess := got[0]["session"].(map[string]any)
	if _, ok := sess["messages"]; ok {
		t.Fatal("the message list must not travel to an agent")
	}
	// Everything an agent reads has to survive.
	for _, want := range []string{"id", "harness", "project", "title", "touched"} {
		if _, ok := sess[want]; !ok {
			t.Errorf("session lost %q", want)
		}
	}
	if len(got[0]["snippets"].([]any)) != 1 {
		t.Error("snippets are the part an agent actually reads")
	}
}
