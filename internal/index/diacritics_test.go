package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	query "github.com/vshulcz/deja-vu/internal/query"
)

// seedOneWord indexes a session holding one word.
func seedOneWord(t *testing.T, word string) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/proj",
		"timestamp": "2026-08-20T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "the word " + word + " in the parser"},
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

// deja folds encodings and does not strip diacritics: one word typed NFC or NFD
// is one word (#1098, #1914), and a word written with its marks is a different
// word from the same one written without them. Nothing on the exact tier joins
// them.
//
// What does join them, for Latin, is the close tier: café and cafe are one edit
// apart, inside its limit of one for a short word. The Arabic pair is out of
// reach twice over — the candidate walk only visits tokens within the limit of
// the query's length, so a three-rune query never sees the six-rune stored form,
// and even if it did the two are three edits apart. One rule, two outcomes.
//
// This records which is which. Note what it cannot tell you: raising the tier's
// edit limit alone would not change the Arabic rows, because the length bucket
// excludes the candidate before any distance is computed.
func TestWhichTierJoinsAWordToItsMarkedForm(t *testing.T) {
	const (
		vowelled = "\u0643\u064e\u062a\u064e\u0628\u064e" // a fatha on each letter
		plain    = "\u0643\u062a\u0628"                   // the same word, as it is normally typed
		accented = "caf\u00e9"
		bare     = "cafe"
	)
	if vowelled == plain || accented == bare {
		t.Fatal("the fixtures are the same string, so the table below says nothing")
	}
	for _, c := range []struct {
		name, stored, query string
		hits                int
		tier                string
	}{
		{"arabic, same form", vowelled, vowelled, 1, query.TierExact},
		{"arabic, marks dropped from the query", vowelled, plain, 0, ""},
		{"arabic, marks added to the query", plain, vowelled, 0, ""},
		{"latin, same form", accented, accented, 1, query.TierExact},
		{"latin, accent dropped from the query", accented, bare, 1, query.TierClose},
		{"latin, accent added to the query", bare, accented, 1, query.TierClose},
	} {
		dir := seedOneWord(t, c.stored)
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
