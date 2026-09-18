package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The suggestion is picked from what people typed as the task. Measured across
// four candidate sources on a 2,000-session store, a phrase recurring across
// session titles is the one that names a subject; a bigram out of the middle of
// a message is a fragment of whatever was being said (#3714).
func TestSuggestPicksAPhraseFromTheTitles(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)

	now := time.Now().UTC()
	write := func(id, title string, rest ...string) {
		var b strings.Builder
		stamp := now.Add(-time.Hour).Format(time.RFC3339)
		lines := append([]string{title}, rest...)
		for _, text := range lines {
			rec := map[string]any{
				"type": "user", "sessionId": id, "cwd": "/work/app", "timestamp": stamp,
				"message": map[string]any{"role": "user", "content": text},
			}
			line, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The task somebody asked for three times, a rarer pair buried in the
	// middle of one session — which is what the old rule would have taken —
	// and three other tasks, so the subject is distinctive among the titles
	// rather than being all of them. A phrase in every title has no rarity
	// left and the floor rejects it, which is the right answer for a store
	// that has only ever been asked one thing.
	write("s1", "the shard rebalance keeps flapping under load",
		"quokka thimble stalls whenever the collector wakes")
	write("s2", "the shard rebalance dropped a replica again")
	write("s3", "why does the shard rebalance take four minutes")
	write("s4", "update the readme wording and the badges")
	write("s5", "bump the linter and fix what it finds")
	write("s6", "write the release notes for this week")
	// An agent-run session opens with the envelope its harness wrote, and its
	// title is that envelope. On a real store the phrase recurring across
	// titles was this family — a fleet's paperwork.
	write("a1", "<teammate-message teammate_id=\"team-lead\">verify the shard rebalance report</teammate-message>")
	write("a2", "<teammate-message teammate_id=\"team-lead\">verify the replica report again</teammate-message>")
	write("a3", "<teammate-message teammate_id=\"team-lead\">verify the collector report once more</teammate-message>")

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := suggestFirstQuery(dir)
	if !strings.Contains(got, "shard") || !strings.Contains(got, "rebalance") {
		t.Fatalf("suggestion = %q, want the task the titles keep coming back to", got)
	}
	if strings.Contains(got, "quokka") || strings.Contains(got, "thimble") {
		t.Errorf("suggestion = %q, which is a phrase from the middle of one session", got)
	}
	if strings.Contains(strings.ToLower(got), "teammate") || strings.Contains(strings.ToLower(got), "verify") {
		t.Errorf("suggestion = %q, which comes from a harness envelope", got)
	}
}

// A store whose titles are all an agent's own reports still gets a suggestion:
// the message scan stays as the fallback.
func TestSuggestFallsBackWhenNoTitleIsAPersons(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	proj := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	write := func(id string, texts ...string) {
		var b strings.Builder
		for _, text := range texts {
			line, err := json.Marshal(map[string]any{
				"type": "user", "sessionId": id, "cwd": "/work/app", "timestamp": stamp,
				"message": map[string]any{"role": "user", "content": text},
			})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Every title is a harness envelope; the bodies carry the real subject.
	write("b1", "<system-reminder>carry on</system-reminder>", "the jwks rotation broke login again")
	write("b2", "<system-reminder>carry on</system-reminder>", "jwks rotation cache is still stale")
	write("b3", "<system-reminder>carry on</system-reminder>", "please update the readme wording")
	write("b4", "<system-reminder>carry on</system-reminder>", "update readme header image")
	write("b5", "<system-reminder>carry on</system-reminder>", "update readme badges please")

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := suggestFirstQuery(dir); !strings.Contains(got, "jwks") {
		t.Fatalf("suggestion = %q, want the fallback to find the subject in the messages", got)
	}
}
