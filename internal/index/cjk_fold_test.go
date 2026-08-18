package index

import (
	"os"
	"path/filepath"
	"testing"

	cjkfold "github.com/vshulcz/deja-vu/internal/cjkfold"
	search "github.com/vshulcz/deja-vu/internal/query"
)

// A Traditional corpus queried in Simplified (and the reverse) must match:
// CJK bigrams are raw codepoints, so without folding 距離 and 距离 are
// unrelated keys and recall is silently one-directional.
func TestCJKFoldCrossScriptSearch(t *testing.T) {
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
	mk("t1", "韓國旅遊簽證的申請進度") // Traditional content
	mk("s1", "刷新令牌怎么实现")    // Simplified content
	mk("j1", "パスワードの再設定")   // Japanese must be untouched
	mk("k1", "비밀번호 재설정")    // Korean must be untouched
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		query string
		want  string
		note  string
	}{
		{"簽證", "t1", "Traditional query, Traditional content"},
		{"签证", "t1", "Simplified query reaches Traditional content"},
		{"申請進度", "t1", "multi-bigram Traditional phrase"},
		{"申请进度", "t1", "multi-bigram Simplified query, Traditional content"},
		{"令牌", "s1", "Simplified query, Simplified content"},
		{"令牌", "s1", "unchanged for content that was already Simplified"},
		{"パスワード", "j1", "Katakana is not folded"},
		{"비밀번호", "k1", "Hangul is not folded"},
	}
	for _, c := range cases {
		got, err := Search(dir, search.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatalf("%s: query %q: %v", c.note, c.query, err)
		}
		var found bool
		for _, s := range got {
			if s.ID == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: query %q did not reach %s (got %d sessions)", c.note, c.query, c.want, len(got))
		}
	}
}

// The fold is a pure Traditional-to-Simplified function: only runes with a
// single unambiguous simplification are listed, non-Han scripts pass through,
// and folding an already-Simplified string is a no-op.
func TestCJKFoldUnit(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"簽證", "签证"},
		{"刺蝟的距離", "刺猬的距离"},
		{"升學計劃", "升学计划"},
		{"签证", "签证"},                   // already Simplified
		{"hello world", "hello world"}, // Latin untouched
		{"パスワード", "パスワード"},             // Katakana untouched
		{"비밀번호", "비밀번호"},               // Hangul untouched
		{"混合 mixed 文字", "混合 mixed 文字"}, // already-Simplified mixed
	} {
		if got := cjkfold.String(c.in); got != c.want {
			t.Errorf("cjkfold.String(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Bigrams keep the script they were written in — the function-word filter
	// depends on that, because 係 folds to 系 which is content in 系統.
	if got := cjkfold.Bigrams("韓國簽證"); got[0] != "韓國" {
		t.Errorf("bigrams should stay unfolded, got %v", got)
	}
	// The keys built from them are what must agree across scripts.
	trad := indexKeys("韓國簽證")
	simp := indexKeys("韩国签证")
	tradBigrams := map[string]bool{}
	for _, k := range trad {
		if len([]rune(k)) == 3 { // "t" + two CJK runes
			tradBigrams[k] = true
		}
	}
	for _, k := range simp {
		if len([]rune(k)) != 3 {
			continue
		}
		if !tradBigrams[k] {
			t.Errorf("key %q from the Simplified spelling is absent for the Traditional one: %v vs %v", k, trad, simp)
		}
	}
}

// Folding changes posting keys, so an index written before it holds keys this
// build never queries. The version gate is what turns that into an automatic
// rebuild instead of silent zero recall for CJK.
func TestFoldRequiresAnIndexVersionOfItsOwn(t *testing.T) {
	if version < 16 {
		t.Fatalf("index version is %d: Traditional folding changes keys and needs its own version", version)
	}
}
