package index

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// On a store where every word of the question is common, the sessions that
// hold all of them across many turns fill the relevance pool, and the one
// session that says the question in its own words sat behind the pool with
// its ten places of strict lead: 41st on a 3,000-session Russian store, where
// a word matches in any of its forms (#5369).
func TestTheOneSessionSayingTheWholeQuestionReachesThePageOnACommonWordStore(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 421)
	for i := range 120 {
		s := model.Session{ID: fmt.Sprintf("decoy%03d", i), Harness: "claude", Project: "p", Updated: time.Now()}
		for j := range 30 {
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: fmt.Sprintf("снова лимит запроса и шлюз, заметка %d", j)})
		}
		sessions = append(sessions, s)
	}
	for i := range 300 {
		sessions = append(sessions, model.Session{ID: fmt.Sprintf("other%03d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: "заметки о планировании недели"}}})
	}
	target := model.Session{ID: "answer", Harness: "claude", Project: "p", Updated: time.Now().Add(-time.Hour)}
	for j := range 40 {
		target.Messages = append(target.Messages, model.Message{Role: "user", Text: fmt.Sprintf("пункт планирования %d", j)})
	}
	target.Messages = append(target.Messages, model.Message{Role: "user", Text: "лимиты запросов к нашему шлюзу подняли до 500"})
	sessions = append(sessions, target)
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	result, err := SearchDetailed(dir, search.Options{Query: "лимиты запросов шлюзу", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.Sessions))
	for _, s := range result.Sessions {
		ids = append(ids, s.ID)
	}
	if result.Strict != 1 {
		t.Fatalf("control: %d sessions hold every word as asked, want 1 (%s)", result.Strict, strings.Join(ids, " "))
	}
	if at := slices.Index(ids, "answer"); at < 0 || at >= 5 {
		t.Errorf("the session saying the whole question is at %d, not in the top 5: %s", at+1, strings.Join(ids[:min(len(ids), 12)], " "))
	}
}

// A message holding the question's words side by side outscores one holding
// them spread through a sentence, and one missing a word gets no lift.
func TestAMessageHoldingTheWordsTogetherScoresAbove(t *testing.T) {
	mt := newMessageTerms([]string{"retry", "budget", "gateway"}, map[string]float64{"retry": 1, "budget": 1, "gateway": 1}, false)
	one := func(text string) model.Session {
		return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
	}
	together := mt.best(one("the gateway retry budget is 500"))
	apart := mt.best(one("the retry loop waits, and after the deploy the budget for the gateway is 500"))
	missing := mt.best(one("the retry budget is 500"))
	if together <= apart {
		t.Errorf("together %.2f, spread %.2f", together, apart)
	}
	if apart <= missing {
		t.Errorf("all three spread %.2f, two of them %.2f", apart, missing)
	}
	if missing != 2 {
		t.Errorf("a message missing a word scored %.2f, want its two words' idf", missing)
	}
	if w := mt.window("Gateway, then RETRY: budget"); w != 4 {
		t.Errorf("window = %d, want 4", w)
	}
}
