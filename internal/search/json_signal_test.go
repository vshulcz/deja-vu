package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	var ss []model.Session
	for i := 0; i < 40; i++ {
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
