package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// docs/json-output.md tells a consumer to branch on the envelope's tier and
// lists "stemmed" among its values. Retrieval reports the coarse "close"
// alongside a stemmed flag, and that overwrote the finer name — so a word form
// and a misspelling, which deja narrates differently on stderr, arrived as the
// same tier (#1616).
func TestStemmedAnswerReportsTheStemmedTier(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the retry loop keeps firing"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1a2b3c4-1111-4000-8000-d6e7f8a9b0c1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1a2b3c4-1111-4000-8000-d6e7f8a9b0c1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", false, nil); err != nil {
		t.Fatal(err)
	}

	tierOf := func(q string) string {
		t.Helper()
		out, err := captureRun(t, "--json", "--no-embed", q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		var env struct {
			Tier    string `json:"tier"`
			Stemmed bool   `json:"stemmed"`
			Fuzzy   bool   `json:"fuzzy"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s: %v\n%s", q, err, out)
		}
		if q == "retries" && !env.Stemmed {
			t.Fatalf("retries was not answered by stemming, so the test measured nothing:\n%s", out)
		}
		return env.Tier
	}
	hitTierOf := func(q string) string {
		t.Helper()
		out, err := captureRun(t, "--json", "--no-embed", q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		var env struct {
			Hits []struct {
				Tier string `json:"tier"`
			} `json:"hits"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s: %v\n%s", q, err, out)
		}
		if len(env.Hits) == 0 {
			t.Fatalf("%s returned no hits, so the test measured nothing:\n%s", q, out)
		}
		return env.Hits[0].Tier
	}
	if got := tierOf("retries"); got != "stemmed" {
		t.Errorf("a word form reports tier %q, not the documented \"stemmed\"", got)
	}
	// The envelope's tier and the hit's are the same idea at two scopes, so a
	// consumer must not read one thing in the envelope and another in the hit.
	if got := hitTierOf("retries"); got != "stemmed" {
		t.Errorf("the hit reports tier %q while the envelope says stemmed", got)
	}
	if got := hitTierOf("retyr"); got != "close" {
		t.Errorf("the hit for a misspelling reports tier %q, not \"close\"", got)
	}
	// The controls: a misspelling is still close, and an exact hit is exact.
	if got := tierOf("retyr"); got != "close" {
		t.Errorf("a misspelling reports tier %q, not \"close\"", got)
	}
	if got := tierOf("retry"); got != "exact" {
		t.Errorf("an exact hit reports tier %q", got)
	}
}
