package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

func TestRussianFillerDoesNotAnchor(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(id, text string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", "починил ретраи очереди доставки для отчетов")
	mk("b", "давай сделай все красиво и покажи мне это")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The filler-only session must not outrank content on a question full of
	// Russian glue: every meaningful word points at session a.
	got, err := Search(dir, search.Options{Query: "давай посмотри как мы делали ретраи доставки", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "a" {
		t.Fatalf("content session must rank first, got %d sessions", len(got))
	}
	terms := RelevanceTerms("давай сделай все по шагам и говори мне")
	for _, term := range terms {
		if term == "давай" || term == "сделай" || term == "говори" {
			t.Fatalf("filler survived RelevanceTerms: %v", terms)
		}
	}
}
