package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bucket() sliced two bytes, so every non-ASCII token — all of Russian, all
// of Chinese, all of Greek — collapsed into the single bucket "t_" and each
// lookup scanned the entire non-ASCII vocabulary.
func TestBucketsShardNonASCIITokens(t *testing.T) {
	words := []string{"миграция", "репликация", "сеть", "база", "ключ", "ошибка", "запрос", "тест", "индекс", "поиск"}
	seen := map[string]bool{}
	for _, w := range words {
		seen[bucket("t"+w)] = true
	}
	if len(seen) < 5 {
		t.Fatalf("%d Russian words landed in %d buckets: %v", len(words), len(seen), seen)
	}
	cjk := []string{"数据库", "迁移", "网络", "错误", "请求", "测试"}
	seen = map[string]bool{}
	for _, w := range cjk {
		seen[bucket("t"+w)] = true
	}
	if len(seen) < 4 {
		t.Fatalf("%d Chinese words landed in %d buckets: %v", len(cjk), len(seen), seen)
	}
	// ASCII sharding must be byte-identical to the old scheme, or every
	// English token silently moves house on upgrade.
	for _, tok := range []string{"tmigration", "tdatabase", "t203", "tpkg/index", "tx"} {
		if len(tok) >= 2 {
			if got, want := bucket(tok), safe(tok[:2]); got != want {
				t.Fatalf("bucket(%q) = %q, want %q — ASCII layout changed", tok, got, want)
			}
		}
	}
}

// The relevance tier folds a term onto its inflections. Cyrillic terms used to
// be handed to the ASCII stemmer, which appended English suffixes to Russian
// words, so the fold never matched anything (#340 shipped as a no-op).
func TestRussianInflectionsFoldInRelevanceTier(t *testing.T) {
	for _, c := range []struct{ query, indexed string }{
		{"миграция", "миграциями"},
		{"миграциями", "миграция"},
		{"репликация", "репликации"},
		{"агентом", "агент"},
		{"база", "базы"},
		{"кластера", "кластеров"},
		// Third-declension feminine nouns — 11% of the vocabulary, and the
		// paradigm this fold was written for.
		{"сеть", "сети"},
		{"сеть", "сетью"},
		{"жизнь", "жизни"},
		{"ночь", "ночи"},
		{"очередь", "очереди"},
		// Short verb stems must still reach their own paradigm: a guard keyed
		// on stem length alone cannot tell вес (noun) from зна (verb).
		{"знать", "знал"},
		{"знаю", "знать"},
		{"могу", "могли"},
		{"говорит", "говорить"},
		{"знать", "знаю"},
		{"считаем", "считать"},
		// Soft-masculine nouns take the hard endings, unlike the feminines.
		{"автомобиль", "автомобиля"},
		{"писатель", "писателем"},
		// -ость nouns never folded before: "ть" matched ahead of "ь".
		{"новость", "новостей"},
		{"активность", "активности"},
		// Real infinitives are vowel + ть and keep the verb branch.
		{"делать", "делаю"},
		{"работать", "работаю"},
		{"видеть", "видел"},
		// ...and ть-final nouns still fold within their own paradigm.
		{"часть", "части"},
		{"весть", "вести"},
		{"власть", "власти"},
	} {
		forms := stemMatchForms(c.query)
		found := false
		for _, f := range forms {
			if f == c.indexed {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stemMatchForms(%q) does not reach %q: %v", c.query, c.indexed, forms)
		}
		for _, f := range forms {
			if strings.ContainsAny(f, "abcdefghijklmnopqrstuvwxyz") {
				t.Errorf("stemMatchForms(%q) produced a Latin-suffixed form %q", c.query, f)
				break
			}
		}
	}
	// Three-letter function words must not fan out into the ending table.
	for _, w := range []string{"что", "как", "его"} {
		if got := stemMatchForms(w); len(got) != 0 {
			t.Errorf("stemMatchForms(%q) = %v, want none", w, got)
		}
	}
}

// The relevance tier has no catalog gate — whatever the fold invents is looked
// up for real — so an over-eager ending reaches a word from another paradigm
// and recalls a session that has nothing to do with the query. A wrong recall
// costs more than a missed inflection.
func TestRussianFoldDoesNotReachUnrelatedWords(t *testing.T) {
	for _, c := range []struct{ query, unrelated string }{
		{"цель", "целая"},
		{"цель", "целый"},
		{"борись", "борис"},
		{"весом", "весть"},
		{"бить", "битой"},
		{"верь", "вера"},
		{"весь", "веса"},
		{"цель", "целое"},
		{"голубь", "голубой"},
		{"столь", "стол"},
		// A noun can end in "ть" too. Taking the verb branch sent часть to
		// час and весть to вес — the latter being the surviving inverse of
		// весом -> весть.
		{"часть", "час"},
		{"часть", "часа"},
		{"весть", "вес"},
		{"весть", "веса"},
		{"лесть", "лес"},
	} {
		for _, f := range stemMatchForms(c.query) {
			if f == c.unrelated {
				t.Errorf("stemMatchForms(%q) reaches the unrelated %q", c.query, c.unrelated)
				break
			}
		}
	}
}

// Both tiers must fold Russian the same way. The stem tier used to strip and
// re-attach from one shared table, so пусть recalled a session that only said
// пустой — the same defect the relevance tier was fixed for, one tier over.
func TestStemTierFoldsLikeTheRelevanceTier(t *testing.T) {
	for _, c := range []struct{ query, unrelated string }{
		{"пусть", "пустой"},
		{"пусть", "пустая"},
		{"часть", "часто"},
		{"цель", "целая"},
	} {
		for _, f := range cyrSuffixForms(c.query) {
			if f == c.unrelated {
				t.Errorf("stem tier folds %q onto the unrelated %q", c.query, c.unrelated)
				break
			}
		}
	}
	for _, c := range []struct{ query, want string }{
		{"жизнь", "жизни"},
		{"дверь", "двери"},
		{"приятель", "приятеля"},
		{"часть", "части"},
		{"миграция", "миграциями"},
	} {
		found := false
		for _, f := range cyrSuffixForms(c.query) {
			if f == c.want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stem tier lost %q -> %q", c.query, c.want)
		}
	}
}

// End to end through the relevance tier, which is what auto-recall uses: it
// has no close/fuzzy ladder to fall back on, so a dead fold there means a
// Russian prompt silently recalls nothing.
func TestRussianRelevanceFoldsInflections(t *testing.T) {
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
	mk("s1", "обсуждали миграциями базы данных и чинили репликациями слейва")
	// Filler so the target is not the only session in the corpus.
	for i := 0; i < 12; i++ {
		mk(fmt.Sprintf("f%d", i), "обычная рабочая сессия про сборку и деплой контейнеров")
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Nominative singular in the prompt, instrumental plural in the session.
	got, matched, _, _, err := ProjectRelevant(dir, []string{"app"}, []string{"миграция", "репликация"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("relevance tier returned nothing for a Russian prompt")
	}
	if got[0].ID != "s1" || matched[0] == 0 {
		t.Fatalf("top relevant session = %q (matched=%v), want s1 with a match", got[0].ID, matched)
	}
}
