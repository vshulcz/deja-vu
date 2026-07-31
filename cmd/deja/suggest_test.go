package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestSuggestFirstQueryPicksDistinctiveRecentPhrase(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	now := "2026-07-20T10:00:00Z"
	mk := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w/a","timestamp":"` + now + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	// "jwks rotation" recurs in two sessions; filler words dominate the rest.
	files := map[string]string{
		"s1": mk("s1", "the jwks rotation broke login again today"),
		"s2": mk("s2", "jwks rotation cache still stale after deploy"),
		"s3": mk("s3", "please update readme wording and typos"),
		"s4": mk("s4", "update readme header image"),
		"s5": mk("s5", "update readme badges"),
	}
	for id, body := range files {
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := suggestFirstQuery(dir)
	if !strings.Contains(got, "jwks") {
		t.Fatalf("suggestion = %q, want the distinctive recurring phrase", got)
	}
}

func TestSuggestFirstQueryEmptyOnThinCorpus(t *testing.T) {
	hermeticEnv(t)
	if got := suggestFirstQuery(index.DefaultDir()); got != "" {
		t.Fatalf("thin corpus suggested %q", got)
	}
}

func TestSuggestTokenRejectsCodeAndNoise(t *testing.T) {
	for _, tok := range []string{
		"map[string]any{",
		"button_result_hd_price",
		"internal/index/store.go",
		"http://example.com",
		"key=value",
	} {
		if suggestToken(tok) != "" {
			t.Errorf("%q came out of source, not a sentence", tok)
		}
	}
	for _, tok := range []string{"pgbouncer", "задачки", "compaction"} {
		if suggestToken(tok) == "" {
			t.Errorf("%q is a word someone would type", tok)
		}
	}
}

func TestSuggestPhraseTokensMarksGaps(t *testing.T) {
	// "the" is a stop word: the two content words either side of it were not
	// adjacent, and a suggestion built from them would read as a fragment.
	got := suggestPhraseTokens("retry the budget")
	if len(got) != 3 || got[0] == "" || got[1] != "" || got[2] == "" {
		t.Fatalf("got %q, want the dropped word marked", got)
	}
}

// The suggestion is scored on what people typed, not on what the agents wrote
// back. A subject the assistant repeats in every session is still distinctive
// if the person only raised it twice — and the change that made this true is
// also what took the line from 2.2 s to 0.19 s (#625).
func TestSuggestScoresUserTurnsNotAssistantEcho(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-e")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	const now = "2026-07-20T10:00:00Z"
	user := func(id, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/w/e","timestamp":"` + now +
			`","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	assistant := func(id, text string) string {
		return `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/e","timestamp":"` + now +
			`","message":{"role":"assistant","content":"` + text + `"}}` + "\n"
	}
	// The agent echoes "quantum harvester" in all five sessions; the person
	// raised it in two. Counting the echo would score it as common and the
	// suggestion would fall back to filler.
	files := map[string]string{
		"e1": user("e1", "quantum harvester latency spike") + assistant("e1", "the quantum harvester is fine"),
		"e2": user("e2", "quantum harvester retries again") + assistant("e2", "the quantum harvester is fine"),
		"e3": user("e3", "please update readme wording") + assistant("e3", "the quantum harvester is fine"),
		"e4": user("e4", "update readme header image") + assistant("e4", "the quantum harvester is fine"),
		"e5": user("e5", "update readme badges please") + assistant("e5", "the quantum harvester is fine"),
	}
	for id, body := range files {
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := suggestFirstQuery(dir); !strings.Contains(got, "quantum") {
		t.Fatalf("suggestion = %q, want the subject the person raised", got)
	}
}
