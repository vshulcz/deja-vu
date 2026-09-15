package search

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A hit carried its session's whole message list, so the size of an answer was
// the size of the reader's longest transcript: 136 MB for one relevance answer
// on a real store, 50 hits over 140,841 messages. A hit is about the passages
// that matched — its own excerpts are in `snippets`, and `show --json` is the
// surface for a whole session (#3620).
func TestAJSONHitCarriesTheMatchedPassagesOnly(t *testing.T) {
	var ms []model.Message
	for i := 0; i < 400; i++ {
		text := fmt.Sprintf("ordinary line %d about nothing much", i)
		if i == 17 || i == 392 {
			text = fmt.Sprintf("line %d: the billing exporter drops every third retry", i)
		}
		ms = append(ms, model.Message{Role: "assistant", Text: text})
	}
	s := model.Session{Harness: "claude", ID: "s1", Project: "work/app", Messages: ms}

	hits := RelevanceHitsWeighted([]model.Session{s}, []string{"billing", "exporter", "retry"}, nil)
	if len(hits) != 1 {
		t.Fatalf("got %d hits", len(hits))
	}
	var out strings.Builder
	Print(&out, hits, Options{JSON: true})
	var env struct {
		Hits []struct {
			Session struct {
				Messages []model.Message `json:"messages"`
			} `json:"session"`
			MessagesTotal  int  `json:"messages_total"`
			MessagesCapped bool `json:"messages_capped"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(env.Hits) != 1 {
		t.Fatalf("envelope holds %d hits", len(env.Hits))
	}
	h := env.Hits[0]
	if h.MessagesTotal != 400 {
		t.Errorf("messages_total = %d, want 400", h.MessagesTotal)
	}
	if !h.MessagesCapped {
		t.Error("a 400-message session came back with no cap reported")
	}
	if n := len(h.Session.Messages); n == 0 || n > jsonHitMessages {
		t.Errorf("the hit carries %d messages, want 1..%d", n, jsonHitMessages)
	}
	// And they are the ones that matched, not the first or last few.
	joined := ""
	for _, m := range h.Session.Messages {
		joined += m.Text + "\n"
	}
	for _, want := range []string{"line 17:", "line 392:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the matched passage %q is missing:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "ordinary line 200 ") {
		t.Errorf("a message that matched nothing was carried:\n%s", joined)
	}
}

// A session small enough to carry whole is carried whole, and says nothing
// about a cap that was not applied.
func TestAShortSessionIsNotReportedAsCapped(t *testing.T) {
	s := model.Session{Harness: "claude", ID: "s2", Project: "work/app", Messages: []model.Message{
		{Role: "user", Text: "the billing exporter drops every third retry"},
		{Role: "assistant", Text: "capped the retries at three"},
	}}
	hits := RelevanceHitsWeighted([]model.Session{s}, []string{"billing", "exporter", "retry"}, nil)
	var out strings.Builder
	Print(&out, hits, Options{JSON: true})
	body := out.String()
	if strings.Contains(body, "messages_capped") {
		t.Errorf("a two-message session was reported as capped: %s", body)
	}
	if !strings.Contains(body, "capped the retries at three") {
		t.Errorf("the session's own text is missing: %s", body)
	}
}
