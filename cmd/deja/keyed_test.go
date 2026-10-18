package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// keyedStore indexes sessions that talk about a request limit in other forms
// than the query «лимиты запроса» types: «лимит запросов», «лимитом запроса»,
// «лимиты запросов». The first rung finds them by stem key, so the scorer has to
// be told to count any form of the words. withExact adds one session that spells
// the query exactly, the only one a scorer counting the typed forms alone sees.
func keyedStore(t *testing.T, withExact bool) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-w-app")
	texts := map[string]string{
		"k1": "лимит запросов к шлюзу превышен",
		"k2": "поднимем лимиты запросов до тысячи",
		"k3": "лимитом запроса управляет конфиг",
	}
	if withExact {
		texts["k0"] = "лимиты запроса записаны в таблицу"
	}
	for id, txt := range texts {
		writeClaudeFixture(t, filepath.Join(root, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"` + txt + `"}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

var keyedAnswers = []string{"шлюзу", "тысячи", "конфиг", "таблицу"}

func wantAllKeyed(t *testing.T, surface, out string) {
	t.Helper()
	for _, w := range keyedAnswers {
		if !strings.Contains(out, w) {
			t.Errorf("%s: the session that says %q is missing from the answer:\n%s", surface, w, out)
		}
	}
}

// `deja search` over a Cyrillic query that the first rung matched by stem key
// must score every form of the words, not only the typed ones: the sessions
// that say «лимит запросов» are as much the answer as the one that says
// «лимиты запроса», and counting the typed forms alone returned that one only.
func TestSearchCountsEveryFormOfAKeyedQuery(t *testing.T) {
	keyedStore(t, true)
	out, err := captureRun(t, "лимиты запроса")
	if err != nil {
		t.Fatal(err)
	}
	wantAllKeyed(t, "deja search", out)
}

// `deja ctx` builds its block from the top hit, so a keyed match scored by the
// typed forms alone finds nothing at all when no session spells them as typed.
func TestCtxFindsAKeyedMatchInAnotherForm(t *testing.T) {
	keyedStore(t, false)
	out, err := captureRun(t, "ctx", "лимиты запроса")
	if err != nil {
		t.Fatalf("ctx found no session that talks about the request limit: %v", err)
	}
	if !strings.Contains(out, "лимит") {
		t.Fatalf("ctx block does not hold the session:\n%s", out)
	}
}

// The context tool an agent calls (recall_context) takes the same path.
func TestRecallContextFindsAKeyedMatchInAnotherForm(t *testing.T) {
	dir := keyedStore(t, false)
	text, n, _, _, err := recallContextResult(dir, "лимиты запроса", "")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || !strings.Contains(text, "лимит") {
		t.Fatalf("recall_context found %d sessions for a keyed match:\n%s", n, text)
	}
}

// The recall tool ranks through recallRank and must return every form too.
func TestRecallCountsEveryFormOfAKeyedQuery(t *testing.T) {
	dir := keyedStore(t, true)
	text, _, _, _, err := recallTextResult(dir, "лимиты запроса", "", 10, 0, 16384)
	if err != nil {
		t.Fatal(err)
	}
	wantAllKeyed(t, "recall", text)
}

// The interactive search list is the fourth caller of the same tier ladder.
func TestTUISearchCountsEveryFormOfAKeyedQuery(t *testing.T) {
	dir := keyedStore(t, true)
	hits, err := tuiSearch(dir, search.Options{Query: "лимиты запроса", Limit: 80})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 {
		var ids []string
		for _, h := range hits {
			ids = append(ids, h.Session.ID)
		}
		t.Fatalf("the list holds %d sessions (%v), want the 4 that talk about the limit", len(hits), ids)
	}
}

// The exact tier reaches every form of a Cyrillic word through its stem key,
// so no form is left out of the answer, even when the page shows one session:
// the word-forms note stays quiet rather than send the reader to search «лимит»
// for sessions this query already found.
func TestWordFormsNoteSkipsAKeyedCyrillicQuery(t *testing.T) {
	dir := keyedStore(t, true)
	o := query.Options{Query: "лимиты запроса", All: true}
	result, err := index.SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Keyed {
		t.Fatal("the first tier did not match by stem key")
	}
	o.Tier = search.TierExact
	o.FuzzyVariants = result.Variants
	detailed, err := search.RunDetailed(result.Sessions, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed.Hits) < 2 {
		t.Fatalf("exact tier hits = %d, want every form", len(detailed.Hits))
	}
	if note := otherWordFormsNote(dir, o, detailed.Hits[:1]); note != "" {
		t.Fatalf("note = %q, want none for a keyed Cyrillic query", note)
	}
}
