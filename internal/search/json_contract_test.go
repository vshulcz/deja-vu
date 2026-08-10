package search

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
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
		"semantic": true, "variants": true,
		// hit
		"session": true, "count": true, "snippets": true, "score": true,
		"tier_detail": true, "superseded": true, "reused": true, "moved": true,
		"lifecycle": true, "lifecycle_note": true, "lifecycle_at": true,
		// session
		"id": true, "harness": true, "project": true, "path": true, "title": true,
		"started": true, "updated": true, "messages": true, "source": true,
		"touched": true, "agent_title": true, "orig_id": true,
		// source
		"origin": true, "instance": true,
		// message
		"role": true, "text": true, "time": true,
	}
	now := time.Now()
	env := searchJSONEnvelope{
		SchemaVersion: jsonout.Version, Tier: "exact", Total: 1, Capped: true,
		Withheld: 1, Fuzzy: true, Stemmed: true, Semantic: true,
		Variants: map[string][]string{"a": {"b"}},
		Hits: []Hit{{
			Session: model.Session{
				ID: "i", Harness: "claude", Project: "p", Path: "/x", Title: "t",
				Started: now, Updated: now,
				Messages: []model.Message{{Role: "user", Text: "x", Time: now}},
				Source:   &model.Source{Origin: "local", Instance: "w"},
				Touched:  []string{"f"}, AgentTitle: true, OrigID: "o",
				Lifecycle: "accepted", LifecycleNote: "n", LifecycleAt: "2026",
			},
			Count: 1, Snippets: []string{"s"}, Score: 1, Tier: "exact",
			TierDetail: "d", Superseded: "2026", Reused: 1, Moved: "2026",
			Lifecycle: "accepted", LifecycleNote: "n", LifecycleAt: "2026",
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
	// variants is map[queryTerm][]string: its keys are user data, not schema, so
	// its subtree is not walked for schema keys.
	var walk func(v any, underData bool)
	walk = func(v any, underData bool) {
		switch node := v.(type) {
		case map[string]any:
			for k, sub := range node {
				if underData {
					continue
				}
				keys[k] = true
				walk(sub, k == "variants")
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
