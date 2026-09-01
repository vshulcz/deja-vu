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
// The close tier does, in every script. For Latin that fell out of the edit
// limit — café and cafe are one edit apart. Arabic is normally typed without
// harakat, and each mark is a rune of its own, so the same pair sat three edits
// apart and outside the length window besides: one rule, two outcomes, and the
// reader who got nothing was the one whose script writes its vowels as marks
// (#1941). A combining mark is now free on this tier, which is what makes the
// two Arabic rows below the same shape as the two Latin ones.
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
		{"arabic, marks dropped from the query", vowelled, plain, 1, query.TierClose},
		{"arabic, marks added to the query", plain, vowelled, 1, query.TierClose},
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

// The tokenizer is where the Arabic pair was lost before the tier ever ran: a
// mark is not a letter, so it ended the word and the index held single letters
// instead of the word. Hebrew with niqqud is the same shape.
func TestAMarkContinuesTheWordItSitsOn(t *testing.T) {
	for _, c := range []struct {
		name, text, want string
	}{
		{"arabic harakat", "\u0643\u064e\u062a\u064e\u0628\u064e", "\u0643\u064e\u062a\u064e\u0628\u064e"},
		{"hebrew niqqud", "\u05e9\u05b8\u05dc\u05d5\u05b9\u05dd", "\u05e9\u05b8\u05dc\u05d5\u05b9\u05dd"},
		{"thai vowel signs", "\u0e01\u0e34\u0e19", "\u0e01\u0e34\u0e19"},
		{"devanagari matras", "\u0939\u093f\u0928\u094d\u0926\u0940", "\u0939\u093f\u0928\u094d\u0926\u0940"},
	} {
		got := tokens(c.text)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: tokens(%q) = %q, want the one word", c.name, c.text, got)
		}
	}
	// A mark with no letter in front of it still separates, so a stray one
	// cannot glue two words together.
	if got := tokens("alpha \u064e beta"); len(got) != 2 {
		t.Errorf("a leading mark joined words: %q", got)
	}
}

// One marked word in one session must not open the fuzzy tier for every short
// word a reader types. The floor exists to keep three-letter queries and CJK
// bigrams out of variant generation (#338); the exemption for a marked form is
// asked of the term, not of the corpus.
func TestAMarkedWordDoesNotOpenTheFloorForEveryShortQuery(t *testing.T) {
	const vowelled = "\u0643\u064e\u062a\u064e\u0628\u064e"
	dir := seedOneWord(t, vowelled+" the parser")
	// "teh" is one edit from "the", which the corpus holds. It is three runes,
	// so it sits below the floor and must find nothing.
	res, err := SearchDetailed(dir, query.Options{Query: "teh", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Errorf("a three-letter typo was expanded because the corpus held an Arabic word: tier=%q variants=%v",
			res.Tier, res.Variants)
	}
	// The Arabic word itself is still reachable from the way it is typed.
	res, err = SearchDetailed(dir, query.Options{Query: "\u0643\u062a\u0628", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 {
		t.Errorf("the marked word became unreachable: %d hits tier=%q", len(res.Sessions), res.Tier)
	}
}

// Thai tone marks are the whole word: ขาว is white, ข่าว is news, ข้าว is rice.
// A mark being free on the close tier joins them there — that is the tier's
// job — but each must answer its own query exactly, and a word that only lost
// its marks must not outrank a real correction.
func TestThaiToneMarksStillTellWordsApart(t *testing.T) {
	const (
		white = "\u0e02\u0e32\u0e27"
		news  = "\u0e02\u0e48\u0e32\u0e27"
		rice  = "\u0e02\u0e49\u0e32\u0e27"
	)
	dir := seedOneWord(t, rice)
	res, err := SearchDetailed(dir, query.Options{Query: rice, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || res.Tier != query.TierExact {
		t.Errorf("rice did not answer its own query exactly: %d hits tier=%q", len(res.Sessions), res.Tier)
	}
	for _, other := range []string{white, news} {
		res, err := SearchDetailed(dir, query.Options{Query: other, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Tier == query.TierExact {
			t.Errorf("a different Thai word answered on the exact tier: %q", other)
		}
	}
}

// A variation selector says how to draw the rune in front of it. It is a mark
// by category, and gluing it on made an ordinary word a different token.
func TestAVariationSelectorIsNotPartOfTheWord(t *testing.T) {
	if got := tokens("widget\ufe0f"); len(got) != 1 || got[0] != "widget" {
		t.Errorf("tokens(widget+VS16) = %q, want the plain word", got)
	}
	if got := tokens("press 1\ufe0f\u20e3 to continue"); len(got) == 0 {
		t.Fatal("no tokens")
	}
}
