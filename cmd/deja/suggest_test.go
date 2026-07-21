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
