package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

func TestQueryKeysCarryTheOtherScript(t *testing.T) {
	has := func(keys []string, prefix, member string) bool {
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) && strings.Contains(k, "|") {
				for _, part := range strings.Split(k, "|") {
					if part == member {
						return true
					}
				}
			}
		}
		return false
	}
	if got := queryKeysLoan("", "деплой запрещен", true); !has(got, "sдепл", "tdeploy") || !has(got, "sдепл", "tdeployed") {
		t.Errorf("no OR group for деплой in %q", got)
	}
	// A Latin word is widened beside a Russian one only; alone it keeps its key.
	if got := queryKeysLoan("", "deploy пятница", true); !has(got, "tdeploy", "sдепл") {
		t.Errorf("no OR group for deploy in %q", got)
	}
	if got := queryKeysLoan("", "deploy friday", true); !reflect.DeepEqual(sortedCopy(got), []string{"tdeploy", "tfriday"}) {
		t.Errorf("English keys changed: %q", got)
	}
	// The relevance tier keeps plain keys.
	for _, k := range queryKeysFor("деплой", true) {
		if strings.Contains(k, "|") {
			t.Errorf("plain key %q has a group", k)
		}
	}
	// An old index has no stem keys and no group; English stays plain keys.
	for _, k := range queryKeysLoan("", "деплой", false) {
		if strings.Contains(k, "|") {
			t.Errorf("old index key %q", k)
		}
	}
	if got := queryKeysLoan("", "rate limit", true); !reflect.DeepEqual(sortedCopy(got), []string{"tlimit", "trate"}) {
		t.Errorf("English keys changed: %q", got)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// A session in one script is reached from a query in the other, both ways.
func TestLoanwordQueryReachesTheOtherScript(t *testing.T) {
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
	mk("ru", "запрещён деплой в пятницу вечером")
	mk("en", "friday evening deploy is forbidden")
	mk("other", "лимиты запроса и квоты")
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
	// A Russian word reaches both scripts, a Latin one beside a Russian word
	// does too, and a second word still narrows by AND. An English query keeps
	// its answer.
	for q, want := range map[string][]string{
		"деплой":         {"en", "ru"},
		"деплоя пятница": {"ru"},
		"deploy пятница": {"ru"},
		"deploy":         {"en"},
		"deploy friday":  {"en"},
	} {
		if got := ids(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
}

func nameStore(t *testing.T, texts map[string]string) (string, func(q string) []string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range texts {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, func(q string) []string {
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
}

// A name spelled in one script is found by its spelling in the other. A Latin
// name is widened beside a Russian word only, as a loanword is: alone it is an
// English query and keeps its answer.
func TestAProperNameIsFoundInTheOtherScript(t *testing.T) {
	_, ids := nameStore(t, map[string]string{
		"ru":    "Нетаньяху выступил на заседании",
		"en":    "Netanyahu spoke at the meeting",
		"other": "обсуждали бюджет проекта",
	})
	for q, want := range map[string][]string{
		"Нетаньяху":             {"en", "ru"},
		"Нетаньяху ":            {"en", "ru"},
		"Netanyahu заседании":   {"ru"},
		"netanyahu заседании":   {"ru"},
		"Netanyahu":             {"en"},
		"Netanyahu the meeting": {"en"},
	} {
		if got := ids(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
}

// A word the store already says often is not widened: its own hits are plenty.
func TestACommonWordIsNotTransliterated(t *testing.T) {
	texts := map[string]string{"cyr": "поехали в парис на неделю"}
	for i := 0; i < translitBelow+2; i++ {
		texts[fmt.Sprintf("p%02d", i)] = "paris in the spring"
	}
	_, ids := nameStore(t, texts)
	for _, id := range ids("paris неделю") {
		if id == "cyr" {
			t.Errorf("paris was transliterated although it hits %d sessions", translitBelow+2)
		}
	}
}

// An English-only query is never widened into Russian, even by a rare word
// that transliterates to one the store holds: "most" is not «мост».
func TestAnEnglishQueryIsNotTransliterated(t *testing.T) {
	_, ids := nameStore(t, map[string]string{
		"en": "the pole position and the most recent build",
		"ru": "мост через реку и поле за домом",
	})
	for _, q := range []string{"most", "pole", "most recent"} {
		if got := ids(q); !reflect.DeepEqual(got, []string{"en"}) {
			t.Errorf("%q = %v, want [en]", q, got)
		}
	}
}

// An English word whose Russian loan has a short stem reaches the loan's own
// forms, not every word that stems the same: test finds тесты, not тесто.
func TestAShortLoanStemDoesNotReachLookalikes(t *testing.T) {
	_, ids := nameStore(t, map[string]string{
		"qa":   "тесты падают, ветка main",
		"cook": "тесто для пирога, ветка main",
		"en":   "the tests are failing, ветка main",
	})
	if got := ids("tests ветка"); !reflect.DeepEqual(got, []string{"en", "qa"}) {
		t.Errorf("tests ветка = %v, want [en qa]", got)
	}
}

// An English query keeps the answer it had before Russian came in: the loanword
// table widens a Latin word only beside a Russian one, so "hook" still reaches
// "hooks" instead of stopping on «хуки».
func TestAnEnglishQueryIsNotWidenedIntoRussian(t *testing.T) {
	_, ids := nameStore(t, map[string]string{
		"en": "the hooks were broken",
		"ru": "хуки сломались",
	})
	if got := ids("hook"); !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("hook = %v, want [en]", got)
	}
	if got := ids("хуки"); !reflect.DeepEqual(got, []string{"en", "ru"}) {
		t.Errorf("хуки = %v, want [en ru]", got)
	}
}

// A transliteration the store never wrote is not added to the query: the
// answer would name a made-up variant.
func TestATransliterationTheStoreLacksIsNotAVariant(t *testing.T) {
	dir, _ := nameStore(t, map[string]string{
		"en": "Netanyahu спорил о бюджете",
	})
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "Netanyahu спорил", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for term, forms := range r.Variants {
		for _, f := range forms {
			if strings.HasPrefix(f, "нетан") {
				t.Errorf("variants[%q] holds %q, which no session says", term, f)
			}
		}
	}
	if len(r.Sessions) != 1 {
		t.Errorf("sessions = %d, want 1", len(r.Sessions))
	}
}
