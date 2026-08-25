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

// The close tier walks only the length buckets within its edit limit of the
// query's length, which is what puts an Arabic word's marked form out of reach
// (#1941). This is what that bucket does to ordinary English: nearly nothing.
// Suffixes, hyphens and a mistyped long word all still meet.
//
// The one direction it closes is a query token longer than the stored one by
// more than the limit — `parser.go` against `parser`. The reverse works through
// a different door: the index splits a filename into its parts, so a query for
// the stem meets a sub-token on the exact tier.
func TestWhatTheCloseTierStillReaches(t *testing.T) {
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
