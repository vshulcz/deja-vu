package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/jsonout"
)

type searchEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Tier          string `json:"tier"`
	Total         int    `json:"total"`
	Capped        bool   `json:"capped"`
	Hits          []struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	} `json:"hits"`
}

func decodeEnvelope(t *testing.T, out string) searchEnvelope {
	t.Helper()
	var env searchEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("search json: %v\n%s", err, out)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version = %d, want %d\n%s", env.SchemaVersion, jsonout.Version, out)
	}
	return env
}

// total and capped are the two questions a caller cannot answer from a list of
// hits, and on the relevance tier both used to answer about the ranking window
// instead of the match: 50 sessions returned, "total": 50, "capped": false,
// whatever the pool behind it was. A consumer reading that stops checking at
// the exact moment there is something to check.
func TestRelevanceEnvelopeReportsThePreCapTotal(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// 60 sessions carry three of the four query terms and one carries only the
	// fourth: every term resolves in the corpus, no session holds them all —
	// which is what drops the ladder to the relevance tier — and the scored
	// pool is 61, deeper than the window it is served through.
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("rel%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "relevance", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"kubernetes autoscaler telemetry notes ` + id + `"}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "relevance", "dash.jsonl"), "dash", []string{
		`{"type":"user","sessionId":"dash","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"dashboards"}}`,
	})

	const q = "kubernetes autoscaler telemetry dashboards"
	out, err := captureRun(t, "--json", "--no-embed", q)
	if err != nil {
		t.Fatal(err)
	}
	env := decodeEnvelope(t, out)
	if env.Tier != "relevance" {
		t.Fatalf("tier = %q, want relevance — the fixture stopped exercising the path\n%s", env.Tier, out)
	}
	if len(env.Hits) != 50 {
		t.Fatalf("hits = %d, want the relevance window of 50", len(env.Hits))
	}
	if env.Total != 61 || !env.Capped {
		t.Fatalf("total = %d capped = %v, want 61 and true: 11 scored sessions were withheld", env.Total, env.Capped)
	}

	// --all lifts the result cap, not a window that lives in retrieval. The
	// point of saying so here is that a consumer passing --all to escape the
	// cap must still read capped rather than assume it escaped one.
	out, err = captureRun(t, "--json", "--no-embed", "--all", q)
	if err != nil {
		t.Fatal(err)
	}
	if env = decodeEnvelope(t, out); env.Total != 61 || !env.Capped || len(env.Hits) != 50 {
		t.Fatalf("--all: total = %d capped = %v hits = %d, want 61/true/50", env.Total, env.Capped, len(env.Hits))
	}

	// The exact tier keeps its own window and its own numbers: one session
	// holds this word, and the cap never came near it.
	out, err = captureRun(t, "--json", "--no-embed", "dashboards")
	if err != nil {
		t.Fatal(err)
	}
	if env = decodeEnvelope(t, out); env.Tier != "exact" || env.Total != 1 || env.Capped || len(env.Hits) != 1 {
		t.Fatalf("exact: tier = %q total = %d capped = %v hits = %d", env.Tier, env.Total, env.Capped, len(env.Hits))
	}

	// Nothing matched is still an answer with a shape: the envelope, a total of
	// zero and no capped, rather than the bare array this used to fall back to.
	out, err = captureRun(t, "--json", "--no-embed", "zzqqxx")
	if err != nil {
		t.Fatal(err)
	}
	if env = decodeEnvelope(t, out); env.Total != 0 || env.Capped || len(env.Hits) != 0 {
		t.Fatalf("empty: total = %d capped = %v hits = %d\n%s", env.Total, env.Capped, len(env.Hits), out)
	}
}
