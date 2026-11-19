package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
	"time"
)

// docs/json-output.md is the contract deja publishes for `search --json`. This
// pins the emitted key set to that document in both directions: a new field the
// doc does not mention fails here (additive changes must be documented), and a
// renamed or removed field drops a required key. It marshals the same envelope
// the JSON path builds, with every optional field populated so all of them show.
func TestSearchJSONKeysMatchTheDocumentedContract(t *testing.T) {
	documented := map[string]bool{
		// envelope
		"schema_version": true, "tier": true, "total": true, "capped": true,
		"policy_withheld": true, "hits": true, "fuzzy": true, "stemmed": true,
		"strict":   true,
		"semantic": true, "variants": true,
		// hit
		"session": true, "count": true, "snippets": true, "score": true,
		"tier_detail": true, "superseded": true, "reused": true, "moved": true,
		"lifecycle": true, "lifecycle_note": true, "lifecycle_at": true,
		// session
		"id": true, "harness": true, "project": true, "path": true, "title": true,
		"started": true, "updated": true, "messages": true, "source": true,
		"touched": true, "agent_title": true, "orig_id": true,
		"model": true, "provider": true,
		"tokens": true, "input": true, "output": true, "reasoning": true, "cache_read": true,
		"tool_calls": true, "tool_errors": true,
		// source
		"origin": true, "instance": true,
		// message
		"role": true, "text": true, "time": true,
	}
	now := time.Now()
	env := searchJSONEnvelope{
		SchemaVersion: jsonout.Version, Tier: "exact", Total: 1, Strict: 1, Capped: true,
		Withheld: 1, Fuzzy: true, Stemmed: true, Semantic: true,
		Variants: map[string][]string{"a": {"b"}},
		Hits: []Hit{{
			Session: model.Session{
				ID: "i", Harness: "claude", Project: "p", Path: "/x", Title: "t",
				Started: now, Updated: now,
				Messages: []model.Message{{Role: "user", Text: "x", Time: now}},
				Source:   &model.Source{Origin: "local", Instance: "w"},
				Touched:  []string{"f"}, AgentTitle: true, OrigID: "o",
				Model:      &model.Model{Provider: "anthropic", ID: "claude-opus-4-6"},
				Tokens:     &model.Tokens{Input: 10, Output: 5, Reasoning: 2, CacheRead: 1},
				ToolCalls:  map[string]int{"Bash": 1},
				ToolErrors: 1,
				Lifecycle:  "accepted", LifecycleNote: "n", LifecycleAt: "2026",
			},
			Count: 1, Snippets: []string{"s"}, Score: 1, Tier: "exact",
			TierDetail: "d", Superseded: "2026", Reused: 1, Moved: "2026",
			Lifecycle: "accepted", LifecycleNote: "n", LifecycleAt: "2026",
			Strict: true,
		}},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	// variants is map[queryTerm][]string and tool_calls is map[toolName]int:
	// their keys are user data, not schema, so their subtrees are not walked for
	// schema keys.
	var walk func(v any, underData bool)
	walk = func(v any, underData bool) {
		switch node := v.(type) {
		case map[string]any:
			for k, sub := range node {
				if underData {
					continue
				}
				keys[k] = true
				walk(sub, k == "variants" || k == "tool_calls")
			}
		case []any:
			for _, sub := range node {
				walk(sub, underData)
			}
		}
	}
	walk(generic, false)
	for k := range keys {
		if !documented[k] {
			t.Errorf("emitted JSON key %q is not in docs/json-output.md — document it or drop it", k)
		}
	}
	for _, req := range []string{
		"schema_version", "tier", "total", "hits",
		"session", "count", "snippets", "score",
		"harness", "id", "messages", "role", "text",
	} {
		if !keys[req] {
			t.Errorf("documented key %q vanished from the emitted JSON", req)
		}
	}
}

// A caller counting recall has to know whether it is looking at matches or at
// the nearest sessions deja could find. That used to be readable only as a
// sentence on stderr, and the JSON shape changed underneath it: exact results
// came back as a bare array, everything else as an object.
func TestJSONAlwaysSaysWhichTierAnswered(t *testing.T) {
	s := model.Session{ID: "s1", Harness: "claude", Messages: []model.Message{{Role: "user", Text: "needle"}}}
	hits := []Hit{{Session: s, Count: 1, Tier: TierRelevance}}

	for name, tc := range map[string]struct {
		opts Options
		want string
	}{
		"exact":     {Options{Query: "needle", JSON: true}, TierExact},
		"relevance": {Options{Query: "needle", JSON: true, Tier: TierRelevance}, TierRelevance},
		"stemmed":   {Options{Query: "needle", JSON: true, Stemmed: true}, TierStemmed},
		"close":     {Options{Query: "needle", JSON: true, Fuzzy: true}, TierClose},
		"semantic":  {Options{Query: "needle", JSON: true, Semantic: true}, TierSemantic},
	} {
		var b bytes.Buffer
		Print(&b, hits, tc.opts)
		var env struct {
			Tier string `json:"tier"`
			Hits []Hit  `json:"hits"`
		}
		if err := json.Unmarshal(b.Bytes(), &env); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, b.String())
		}
		if env.Tier != tc.want {
			t.Fatalf("%s: match = %q, want %q", name, env.Tier, tc.want)
		}
		if len(env.Hits) != 1 {
			t.Fatalf("%s: hits = %d", name, len(env.Hits))
		}
	}
}

// Counting the hits that survive the cap measures the cap. RunDetailed reports
// how many matched before it.
func TestRunDetailedReportsWhatTheCapHides(t *testing.T) {
	ss := make([]model.Session, 0, 40)
	for i := range 40 {
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%02d", i), Harness: "claude",
			Messages: []model.Message{{Role: "user", Text: "needle"}},
		})
	}
	r, err := RunDetailed(ss, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 15 {
		t.Fatalf("returned %d hits, want the default cap of 15", len(r.Hits))
	}
	if r.Total != 40 {
		t.Fatalf("total = %d, want every match", r.Total)
	}
	if !r.Capped {
		t.Fatal("capped is false while 25 matches were withheld")
	}

	all, err := RunDetailed(ss, Options{Query: "needle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Hits) != 40 || all.Capped {
		t.Fatalf("--all returned %d hits, capped=%v", len(all.Hits), all.Capped)
	}
}

// A hit whose decision did not hold says so above its snippets, in words a
// reader can act on without knowing our vocabulary for them.
func TestLifecycleIsPrintedBeforeTheSnippets(t *testing.T) {
	base := model.Session{ID: "s1", Harness: "claude", Project: "payments",
		Messages: []model.Message{{Role: "user", Text: "needle"}}}
	for state, want := range map[string]string{
		"rejected":   "tried and rejected",
		"superseded": "replaced by a later decision",
		"stale":      "may no longer hold",
		"invented":   "invented",
	} {
		hit := Hit{Session: base, Count: 1, Snippets: []string{"needle"},
			Lifecycle: state, LifecycleAt: "2026-07-29", LifecycleNote: "the pin was reverted"}
		var b bytes.Buffer
		Print(&b, []Hit{hit}, Options{Query: "needle"})
		out := b.String()
		if !strings.Contains(out, want) {
			t.Fatalf("%s: %q missing from\n%s", state, want, out)
		}
		if !strings.Contains(out, "2026-07-29") || !strings.Contains(out, "the pin was reverted") {
			t.Fatalf("%s dropped the date or the reason:\n%s", state, out)
		}
		// The marker has to come before the snippet: a reader who stops after
		// one line must not stop on a conclusion that was reverted.
		if strings.Index(out, want) > strings.Index(out, "needle\n") && strings.Contains(out, "needle\n") {
			t.Fatalf("%s printed after the snippet:\n%s", state, out)
		}
	}

	// Nothing recorded, nothing said.
	var b bytes.Buffer
	Print(&b, []Hit{{Session: base, Count: 1, Snippets: []string{"needle"}}}, Options{Query: "needle"})
	for _, unwanted := range []string{"rejected", "replaced", "stale"} {
		if strings.Contains(b.String(), unwanted) {
			t.Fatalf("an unmarked hit mentioned %q:\n%s", unwanted, b.String())
		}
	}
}

// Term frequency ranks these backwards: the session where someone kept asking
// repeats the words most, and the one that answered says them once and then
// explains. Measured on a corpus built for that shape, the deciding session was
// last of four in every case before this existed.
//
// The corpus carries unrelated sessions on purpose. With only the candidates in
// it, every document contains every query term, IDF collapses to zero, BM25
// scores everything 0.0000 and the order is not decided by ranking at all — a
// smaller fixture would have tested nothing.
func TestASessionThatDecidedSomethingOutranksOneThatDidNot(t *testing.T) {
	// Sessions need timestamps: freshnessDecay multiplies an undated session
	// into the floor, and a fixture without dates measures the decay rather
	// than the ranking.
	now := time.Now()
	filler := func(id, text string) model.Session {
		return model.Session{ID: id, Harness: "claude", Project: "app", Started: now, Updated: now,
			Messages: []model.Message{{Role: "user", Text: text, Time: now}}}
	}
	corpus := []model.Session{
		filler("f1", "the deploy pipeline is red again"),
		filler("f2", "renaming the config keys"),
		filler("f3", "who owns the staging database"),
		filler("f4", "we should write down the release checklist"),
		filler("f5", "the linter is complaining about imports"),
	}
	asking := model.Session{ID: "asking", Harness: "claude", Project: "app", Started: now, Updated: now, Messages: []model.Message{
		{Role: "user", Text: "anyone seen prepared statements pgbouncer? prepared statements pgbouncer again today"},
		{Role: "assistant", Text: "still looking at prepared statements pgbouncer, no idea yet"},
	}}
	decided := model.Session{ID: "decided", Harness: "claude", Project: "app", Started: now, Updated: now, Messages: []model.Message{
		{Role: "user", Text: "prepared statements pgbouncer keeps failing"},
		{Role: "assistant", Text: "Root cause was transaction pooling. We pinned pgx to 5.4.3."},
	}}
	hits, err := Run(append(corpus, asking, decided), Options{Query: "prepared statements pgbouncer", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Session.ID != "decided" {
		t.Fatalf("the session that concluded something did not rank first: %s", hits[0].Session.ID)
	}

	// It is a tie-breaker, not an override: a conclusion about something else
	// must not outrank a session that squarely matches the question.
	elsewhere := model.Session{ID: "elsewhere", Harness: "claude", Project: "app", Started: now, Updated: now, Messages: []model.Message{
		{Role: "user", Text: "the icon barrel file"},
		{Role: "assistant", Text: "Root cause was tree shaking. We dropped the barrel file."},
	}}
	onTopic := model.Session{ID: "on-topic", Harness: "claude", Project: "app", Started: now, Updated: now, Messages: []model.Message{
		{Role: "user", Text: "prepared statements pgbouncer prepared statements pgbouncer"},
		{Role: "user", Text: "prepared statements pgbouncer once more"},
	}}
	hits, err = Run(append(corpus, elsewhere, onTopic), Options{Query: "prepared statements pgbouncer", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "on-topic" {
		t.Fatalf("a conclusion about another topic outranked the matching session: %s", hits[0].Session.ID)
	}

	// A user proposing something is not a decision; the record is what the
	// side that did the work wrote.
	proposal := model.Session{ID: "proposal", Harness: "claude", Project: "app", Started: now, Updated: now, Messages: []model.Message{
		{Role: "user", Text: "we should just pin pgx, root cause is obvious"},
	}}
	if decidesSomething(proposal) {
		t.Fatal("a user proposal counted as a decision")
	}
	if !decidesSomething(decided) {
		t.Fatal("an assistant conclusion did not count as a decision")
	}
}

// A pasted log outranks a human answer on term frequency alone: it repeats the
// query words a dozen times and says nothing. The signal is line repetition
// rather than vocabulary — a log repeats its shape, a person explaining
// something at length does not.
func TestAPastedLogRanksBelowAnAnswer(t *testing.T) {
	now := time.Now()
	sess := func(id string, texts ...string) model.Session {
		s := model.Session{ID: id, Harness: "claude", Project: "app", Started: now, Updated: now}
		for i, txt := range texts {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: txt, Time: now})
		}
		return s
	}
	answer := sess("answer",
		"what is going on with connection pool exhausted?",
		"connection pool exhausted only happens on staging after a deploy, never locally")
	paste := sess("paste",
		"pasting the output",
		strings.Repeat("WARN connection pool exhausted staging deploy retry=3 conn=17\n", 14))
	filler := sess("f1", "the linter is complaining about imports")

	hits, err := Run([]model.Session{filler, paste, answer}, Options{Query: "connection pool exhausted staging deploy", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Session.ID != "answer" {
		t.Fatalf("the paste outranked the answer: %s", hits[0].Session.ID)
	}
	// Damped, not hidden: some pastes are the answer.
	found := false
	for _, h := range hits {
		if h.Session.ID == "paste" {
			found = true
		}
	}
	if !found {
		t.Fatal("the paste was dropped from the results entirely")
	}
}

func TestPasteDetectionBoundaries(t *testing.T) {
	msg := func(text string) model.Session {
		return model.Session{Messages: []model.Message{{Role: "assistant", Text: text}}}
	}
	if looksPasted(msg(strings.Repeat("same line\n", 7))) {
		t.Fatal("seven repeated lines counted as a dump; a short list is not a log")
	}
	if !looksPasted(msg(strings.Repeat("same line\n", 14))) {
		t.Fatal("fourteen identical lines did not count as a dump")
	}
	// Long prose with distinct lines is not a dump however long it runs.
	var prose strings.Builder
	for i := range 30 {
		fmt.Fprintf(&prose, "paragraph %d saying something different about the pool\n", i)
	}
	if looksPasted(msg(prose.String())) {
		t.Fatal("thirty distinct lines counted as a dump")
	}
	if looksPasted(model.Session{}) {
		t.Fatal("an empty session counted as a dump")
	}
}
