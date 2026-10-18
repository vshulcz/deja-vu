package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func TestStemKeyOnlyForCyrillicWords(t *testing.T) {
	if st, ok := stemKey("запросов"); !ok || st != "запрос" {
		t.Errorf("stemKey(запросов) = %q, %v", st, ok)
	}
	for _, tok := range []string{"го", "request", "k8sнастройки", "что-то", ""} {
		if _, ok := stemKey(tok); ok {
			t.Errorf("stemKey(%q) should not stem", tok)
		}
	}
}

// A query looks up the Root of its word and a document is filed under the
// Root and one pass more, so a short-stem form (лимит) finds every form of the
// word. The long-stem form (лимиты) finds the forms filed under the same Root.
func TestAnyQueryFormFindsTheForms(t *testing.T) {
	filed := func(doc, key string) bool {
		for _, k := range indexKeys(doc) {
			if k == "s"+key {
				return true
			}
		}
		return false
	}
	groups := [][]string{
		{"лимит", "лимиты", "лимитов", "лимита"},
		{"запрос", "запроса", "запросов", "запросы"},
		{"зелёный", "зеленый", "зелёного", "зелёные"},
	}
	for _, g := range groups {
		q, _ := stemKey(g[0])
		for _, w := range g {
			if !filed(w, q) {
				t.Errorf("query %q (key %q) misses the document form %q: %q", g[0], q, w, indexKeys(w))
			}
		}
	}
	// The forms of a word that Snowball stems consistently share one key.
	for _, g := range groups[1:] {
		want, _ := stemKey(g[0])
		for _, w := range g[1:] {
			if got, _ := stemKey(w); got != want {
				t.Errorf("stemKey(%q) = %q, but stemKey(%q) = %q", w, got, g[0], want)
			}
		}
	}
}

// A second Snowball pass must not shorten a stem to a bare beginning: those keys
// joined words that share only the first letters.
func TestStemKeysKeepEnoughLetters(t *testing.T) {
	for word, want := range map[string]string{"удалённая": "удален", "зелёный": "зелен", "телеграме": "телеграм"} {
		if got, _ := stemKey(word); got != want {
			t.Errorf("stemKey(%q) = %q, want %q", word, got, want)
		}
	}
}

func TestIndexKeysCarryStemKeys(t *testing.T) {
	got := indexKeys("лимиты запроса")
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, want := range []string{"tлимиты", "tзапроса", "sлим", "sзапрос"} {
		if !set[want] {
			t.Errorf("indexKeys missing %q in %q", want, got)
		}
	}
	if k := indexKeys("rate limit"); !reflect.DeepEqual(k, []string{"tlimit", "trate"}) && !reflect.DeepEqual(k, []string{"trate", "tlimit"}) {
		t.Errorf("English keys changed: %q", k)
	}
}

func TestQueryKeysForStems(t *testing.T) {
	if got := queryKeysFor("лимит запросов", true); !reflect.DeepEqual(got, []string{"sзапрос", "sлим"}) && !reflect.DeepEqual(got, []string{"sлим", "sзапрос"}) {
		t.Errorf("stem keys = %q", got)
	}
	if got := queryKeysFor("лимит запросов", false); got[0][0] != 't' {
		t.Errorf("old index keys = %q", got)
	}
	if got := queryKeysFor("rate limit", true); got[0][0] != 't' {
		t.Errorf("English must keep t keys: %q", got)
	}
}

// The first tier answers in any form of the words: "лимиты запроса" reaches a
// session that only says "лимит запросов", and a stem hit on one form no longer
// hides the sessions holding the others.
func TestAnyFormOfAWordFindsEveryForm(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
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
	mk("one", "достигли лимит запросов к API")
	mk("two", "обсуждали лимиты запроса и квоты")
	mk("yo", "Зелёный отчёт готов")
	mk("en", "the rate limit was reached")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	ids := func(q string) []string {
		r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range r.Sessions {
			out = append(out, s.ID)
		}
		sort.Strings(out)
		return out
	}
	want := []string{"one", "two"}
	for _, q := range []string{"лимиты запроса", "лимит запросов", "лимитов запросам"} {
		if got := ids(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"зеленый отчет", "ЗЕЛЁНЫЙ ОТЧЁТ"} {
		if got := ids(q); !reflect.DeepEqual(got, []string{"yo"}) {
			t.Errorf("%q = %v, want [yo]", q, got)
		}
	}
	if got := ids("rate limit"); !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("English query = %v", got)
	}
}

// The word catalog feeds the "other word forms" note and the fuzzy tier; a stem
// key ("s"+stem) is plumbing there, not a word somebody wrote, and the
// did-you-mean check respells a typo with a written word, never with a stem key.
func TestCatalogHoldsWordsNotStemKeys(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "лимиты запросов превышены"}},
	})
	catalog, err := tokenCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !catalog["лимиты"] {
		t.Fatalf("catalog lacks the written word: %d entries", len(catalog))
	}
	if len(catalog) != 3 {
		t.Fatalf("catalog = %v, want the three written words only", catalog)
	}
	if got := DidYouMean(dir, "лимитыы запросов"); got != "лимиты запросов" {
		t.Fatalf("DidYouMean = %q, want the written word «лимиты» put back", got)
	}
}

// stemStore writes one session per text into a fresh store and returns its
// directory and manifest; every session holds the one text as a single message.
func stemStore(t *testing.T, texts map[string]string) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	var ss []model.Session
	for id, text := range texts {
		ss = append(ss, model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{{Role: "user", Text: text}},
		})
	}
	writeTestStore(t, dir, ss...)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version < stemKeysSince {
		t.Fatalf("fixture store is version %d, older than the stem keys (%d)", m.Version, stemKeysSince)
	}
	return dir, m
}

// The relevance tier looks a Cyrillic term up by its stem key alone: every form
// of the word sits on that key, so adding the ending-table forms as OR keys
// counts a message that holds one of them twice. «лимитов» is on the ending
// table of «лимиты» and «лимитам» is not; one message each must score alike.
func TestRelevanceCountsAStemHitOnce(t *testing.T) {
	dir, m := stemStore(t, map[string]string{
		"listed":   "лимитов достаточно для всех",
		"unlisted": "лимитам достаточно для всех",
	})
	rank, err := relevantMetasCounts(dir, m, nil, []string{"лимиты"}, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rank.metas) != 2 || len(rank.scores) != 2 {
		t.Fatalf("ranked %d sessions, want both forms found", len(rank.metas))
	}
	if d := rank.scores[0] - rank.scores[1]; d > 1e-9 || d < -1e-9 {
		t.Fatalf("scores %v for %s, %s: one message in each form must weigh the same", rank.scores, rank.metas[0].ID, rank.metas[1].ID)
	}
}

// A substring query walks the token catalog; a stem key ("s"+stem) is not a
// word, so it must not be offered as a form of the query. «имит» is inside the
// stem «лимит» and inside the written «лимитов», and only the written word
// is a form.
func TestSubstringTierSkipsStemKeys(t *testing.T) {
	dir, _ := stemStore(t, map[string]string{"a": "лимитов достаточно"})
	posts, variants, err := intersectSubstringPostingsDetailed(dir, []string{"имит"})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) == 0 {
		t.Fatal("the substring tier found nothing in «лимитов»")
	}
	if got := variants["имит"]; !reflect.DeepEqual(got, []string{"лимитов"}) {
		t.Fatalf("variants of «имит» = %q, want the written word only", got)
	}
}

// The per-prompt hook probes its candidate queries with FirstMatch; a query in
// one form of a word reaches a session that wrote another, as the search does.
func TestFirstMatchFindsAnotherFormOfTheWord(t *testing.T) {
	dir, _ := stemStore(t, map[string]string{"a": "лимитов достаточно для всех"})
	ss, q, err := FirstMatch(dir, []string{"лимит"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "a" || q != "лимит" {
		t.Fatalf("FirstMatch = %d sessions for %q, want session a for «лимит»", len(ss), q)
	}
}

// The plan builder asks which sessions hold a term; a Cyrillic term is looked up
// by its stem key, so it reaches the session that wrote another form.
func TestPlanTermSessionsReachesAnotherForm(t *testing.T) {
	dir, _ := stemStore(t, map[string]string{"a": "лимитов достаточно для всех"})
	if got := planTermSessions(dir, "лимит"); len(got) != 1 {
		t.Fatalf("planTermSessions(лимит) = %v, want the session that wrote «лимитов»", got)
	}
}

// The record check accepts any form of a query word only on an index that holds
// stem keys: one from before them has no stem to match by, and a Regex query is
// matched as written.
func TestStemVariantsNeedAnIndexWithStemKeys(t *testing.T) {
	o := query.Options{Query: "лимиты"}
	if got := withStemVariants(Manifest{Version: stemKeysSince}, o, nil); !reflect.DeepEqual(got["лимиты"], []string{"лимит"}) {
		t.Fatalf("current index: variants = %v, want the stem of «лимиты»", got)
	}
	if got := withStemVariants(Manifest{Version: stemKeysSince - 1}, o, nil); len(got) != 0 {
		t.Fatalf("index before stem keys: variants = %v, want none", got)
	}
	o.Regex = true
	if got := withStemVariants(Manifest{Version: stemKeysSince}, o, nil); len(got) != 0 {
		t.Fatalf("regex query: variants = %v, want none", got)
	}
}
