package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	query "github.com/vshulcz/deja-vu/internal/query"
)

// seedCleanWord indexes a session whose only content word is the one given, so
// a hit cannot come from a stem sitting elsewhere in the sentence.
func seedCleanWord(t *testing.T, word string) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/proj",
		"timestamp": "2026-08-20T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "we looked at " + word + " yesterday"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `close` is what deja reports for three different mechanisms, and the rows
// below use all three. Worth knowing before reading them as one thing:
//
//   - suffixes reach through the stem tier — "ies" to "y" and the rest of
//     oneSuffixStep — which reports itself as close (retrieval.go:2310);
//   - hyphens and a mistyped long word reach through the fuzzy tier, which is
//     the edit-distance walk that reports the same tier (retrieval.go:2293);
//   - the two exact rows reach because the index tokeniser breaks a run at any
//     rune that is not a letter, a digit, `_` or `-` (retrieval.go:2858), so
//     `parser.go` is stored as `parser` and `go`, and identifier parts are
//     added on top of that — while query.Tokens keeps `parser.go` as one token.
//
// The point of the table is the last row. The edit-distance walk visits only
// the length buckets within its limit of the query's length, which is what puts
// an Arabic word's marked form out of reach (#1941), and in ordinary English it
// closes exactly one direction: a query token longer than the stored one by
// more than that limit. `parser.go` is nine runes, `parser` is six, the buckets
// are seven to eleven, and the two are never compared.
func TestWhatAQueryStillReaches(t *testing.T) {
	for _, c := range []struct {
		name, stored, query string
		hits                int
		tier                string
	}{
		{"suffix, one letter", "parser", "parsers", 1, query.TierClose},
		{"suffix, two letters", "retry", "retries", 1, query.TierClose},
		{"suffix, three letters", "connect", "connecting", 1, query.TierClose},
		{"hyphen dropped", "work-tree", "worktree", 1, query.TierClose},
		{"hyphen added", "worktree", "work-tree", 1, query.TierClose},
		{"long word, one edit", "connection_pool", "connection_poll", 1, query.TierClose},
		{"long word, three edits", "connection_pool", "connectionpool", 1, query.TierClose},
		{"filename against its stem", "parser.go", "parser", 1, query.TierExact},
		{"long name against short stem", "connection_pool_manager.go", "connection", 1, query.TierExact},
		{"stem against its filename", "parser", "parser.go", 0, ""},
	} {
		dir := seedCleanWord(t, c.stored)
		res, err := SearchDetailed(dir, query.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != c.hits {
			t.Errorf("%s: %d hits, want %d", c.name, len(res.Sessions), c.hits)
		}
		if c.hits > 0 && res.Tier != c.tier {
			t.Errorf("%s: answered on the %q tier, want %q", c.name, res.Tier, c.tier)
		}
	}
}
