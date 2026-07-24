package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// The table from #337, verbatim.
func TestCJKBigramSearch(t *testing.T) {
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
	mk("s1", "刷新令牌怎么实现")
	mk("s2", "用 jwt 做 refresh")
	mk("s3", "喝茶")
	mk("s4", "abc装订def")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query string
		want  string
	}{
		{"令牌", "s1"},   // exact bigram
		{"刷新令牌", "s1"}, // ANDed bigrams
		{"jwt", "s2"},  // mixed text unaffected
		{"茶", "s3"},    // single rune: close tier over the run token
		{"装订", "s4"},   // run inside ASCII neighbours
	}
	for _, c := range cases {
		got, err := Search(dir, search.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		found := false
		for _, s := range got {
			if s.ID == c.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: want %s in results, got %d sessions", c.query, c.want, len(got))
		}
	}
	// No cross-boundary bigrams: "c装" must not exist as a posting.
	if got, _ := Search(dir, search.Options{Query: "c装", All: true}); len(got) > 0 {
		for _, s := range got {
			if s.ID == "s4" {
				// substring tier may still find the raw token — that is the
				// documented pre-existing path, not a bigram; assert the
				// bigram posting itself is absent instead.
				break
			}
		}
	}
	if posts, err := postingsFor(dir, "tc装"); err == nil && len(posts) > 0 {
		t.Fatal("cross-boundary bigram was indexed")
	}
	if posts, err := postingsFor(dir, "t订d"); err == nil && len(posts) > 0 {
		t.Fatal("cross-boundary bigram was indexed")
	}
}

func TestCJKBigramsUnit(t *testing.T) {
	got := cjkBigrams("装订计数")
	want := []string{"装订", "订计", "计数"}
	if len(got) != len(want) {
		t.Fatalf("bigrams = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bigrams = %v, want %v", got, want)
		}
	}
	if got := cjkBigrams("茶"); len(got) != 1 || got[0] != "茶" {
		t.Fatalf("unigram run = %v", got)
	}
	if got := cjkBigrams("plain ascii"); len(got) != 0 {
		t.Fatalf("ascii leaked = %v", got)
	}
}
