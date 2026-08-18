package index

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
)

func legacyBucket(tok string) string {
	runes := []rune(tok)
	if len(runes) >= 2 && isShardASCII(runes[0]) && isShardASCII(runes[1]) {
		return safe(string(runes[:2]))
	}
	if len(runes) > 3 {
		runes = runes[:3]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(string(runes)))
	return fmt.Sprintf("x%02x", h.Sum32()%256)
}

func legacyCJKKeys(s string) []string {
	var out []string
	for _, bg := range cjkfold.Bigrams(s) {
		out = append(out, "t"+cjkfold.String(bg))
	}
	return out
}

func collectCJKIndexKeys492(s string) []string {
	var out []string
	cjkIndexKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

func assertSameKeySet492(t *testing.T, input string, got, want []string) {
	t.Helper()

	gotSet := make(map[string]struct{}, len(got))
	for _, key := range got {
		gotSet[key] = struct{}{}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, key := range want {
		wantSet[key] = struct{}{}
	}

	if len(gotSet) != len(wantSet) {
		t.Fatalf("input %q: key-set size differs: got %d (%q), want %d (%q)",
			input, len(gotSet), got, len(wantSet), want)
	}
	for key := range wantSet {
		if _, ok := gotSet[key]; !ok {
			t.Fatalf("input %q: missing key %q: got %q, want %q",
				input, key, got, want)
		}
	}
}

func countKey492(keys []string, want string) int {
	n := 0
	for _, key := range keys {
		if key == want {
			n++
		}
	}
	return n
}

func TestBucketDifferential492(t *testing.T) {
	cases := []struct {
		name string
		tok  string
	}{
		{name: "empty", tok: ""},
		{name: "one ASCII rune", tok: "t"},
		{name: "two ASCII runes", tok: "ta"},
		{name: "three ASCII runes", tok: "tag"},
		{name: "second rune non-ASCII", tok: "t界"},
		{name: "CJK followed by Latin", tok: "界tag"},
		{name: "supplementary Han", tok: "t𠀾"},
		{name: "ASCII punctuation", tok: "t-"},
		{name: "ASCII uppercase and punctuation", tok: "T!"},
		{name: "invalid prefix", tok: "t\xff\xfe"},
		{name: "truncated multibyte prefix", tok: "t\xe8\xa8"},
		{name: "lone continuation byte", tok: "\x80"},
		{name: "lone continuation bytes", tok: "\x80\xbf"},
		{name: "valid RuneError query term", tok: "t\ufffdquery"},
		{name: "RuneError followed by invalid byte", tok: "t\ufffd\xffquery"},
		{name: "invalid third rune after ASCII shard", tok: "ta\xff"},
		{name: "invalid byte after hashed prefix", tok: "t界\xff"},
		{name: "invalid byte beyond hashed prefix", tok: "t界語\xff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bucket(tc.tok)
			want := legacyBucket(tc.tok)
			if got != want {
				t.Fatalf("bucket(%q) = %q, legacyBucket = %q; bytes: % x",
					tc.tok, got, want, []byte(tc.tok))
			}
		})
	}
}

func TestCJKIndexKeysDifferential492(t *testing.T) {
	longRun := strings.Repeat("漢字仮名", 24)
	if len(longRun) <= 64 {
		t.Fatalf("long-run fixture is only %d bytes", len(longRun))
	}

	cases := []struct {
		name string
		text string
	}{
		{name: "ASCII guard", text: "plain ASCII text only"},
		{name: "fold collision", text: "關係 关系"},
		{name: "single-rune runs", text: "茶 A 山"},
		{name: "two-rune run", text: "装订"},
		{name: "supplementary-plane Han", text: "𠀾界𠀀"},
		{name: "kana only", text: "ひらがなカタカナ"},
		{name: "Hangul only", text: "한글만사용"},
		{name: "mixed CJK and Latin boundaries", text: "abc装订def 관계 xyz"},
		{name: "long CJK run across tokenizer byte boundary", text: longRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectCJKIndexKeys492(tc.text)
			want := legacyCJKKeys(tc.text)
			assertSameKeySet492(t, tc.text, got, want)
		})
	}
}

func TestCJKIndexKeysFoldCollision492(t *testing.T) {
	traditional := "關係"
	simplified := "关系"
	if cjkfold.String(traditional) != cjkfold.String(simplified) {
		t.Fatalf("fixture does not fold to one key: %q -> %q, %q -> %q",
			traditional, cjkfold.String(traditional),
			simplified, cjkfold.String(simplified))
	}

	input := traditional + " " + simplified
	got := collectCJKIndexKeys492(input)
	want := legacyCJKKeys(input)
	assertSameKeySet492(t, input, got, want)

	collisionKey := "t" + cjkfold.String(traditional)
	if n := countKey492(want, collisionKey); n != 2 {
		t.Fatalf("legacy fixture should emit collision key twice, got %d in %q", n, want)
	}
	if n := countKey492(got, collisionKey); n != 1 {
		t.Fatalf("new emitter should deduplicate collision key, got %d in %q", n, got)
	}
}

// The byte-identical buckets/ check testifies for ingestion only. The query
// side still depends on cjkBigrams emitting unfolded bigrams — the
// function-word filter must see the runes the user typed — and that has no
// index artifact to diff, so this pins it directly: Traditional-script input
// through expandCJKTokens keeps its script, before and after the ingestion
// emitter split (#492).
func TestExpandCJKTokensKeepsScript492(t *testing.T) {
	got := expandCJKTokens([]string{"韓國簽證"})
	want := []string{"韓國", "國簽", "簽證"}
	if len(got) != len(want) {
		t.Fatalf("expandCJKTokens(韓國簽證) = %q, want %q", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("expandCJKTokens(韓國簽證)[%d] = %q, want %q (folded or reordered)", i, got[i], w)
		}
	}
	if f := cjkfold.String("韓國"); f == "韓國" {
		t.Fatalf("fixture lost its point: %q does not fold", "韓國")
	}
}

// The benchmarks pit the new emitter and shard function against the legacy
// copies above inside one binary, so the comparison is immune to the
// binary-layout variance that dominates whole-build wall clocks on small
// corpora (#492).

var benchJAText = strings.Repeat("昨日のビルドで漢字の索引が遅くなった原因を調べた。設定を確認し、キャッシュを削除した。", 4)

func BenchmarkCJKKeysLegacy(b *testing.B) {
	for i := 0; i < b.N; i++ {
		keys := legacyCJKKeys(benchJAText)
		if len(keys) == 0 {
			b.Fatal("no keys")
		}
	}
}

func BenchmarkCJKKeysNew(b *testing.B) {
	for i := 0; i < b.N; i++ {
		n := 0
		cjkIndexKeys(benchJAText, func(string) { n++ })
		if n == 0 {
			b.Fatal("no keys")
		}
	}
}

var benchBucketToks = []string{"tconfig", "t設定", "t漢字", "tjanuary", "tcache", "t索引", "t2026-01", "tビル"}

func BenchmarkBucketLegacy(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, tok := range benchBucketToks {
			if legacyBucket(tok) == "" {
				b.Fatal("empty bucket")
			}
		}
	}
}

func BenchmarkBucketNew(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, tok := range benchBucketToks {
			if bucket(tok) == "" {
				b.Fatal("empty bucket")
			}
		}
	}
}

func TestCJKIndexDifferentialGenerative492(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	t.Run("bucket random bytes", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			b := make([]byte, rng.Intn(32))
			for j := range b {
				b[j] = byte(rng.Intn(256))
			}
			if len(b) > 0 && i%5 == 0 {
				b[0] = 0xff
			}

			tok := string(b)
			got := bucket(tok)
			want := legacyBucket(tok)
			if got != want {
				t.Fatalf("case %d: bucket bytes % x = %q, legacyBucket = %q",
					i, b, got, want)
			}
		}
	})

	t.Run("CJK and ASCII mixes", func(t *testing.T) {
		alphabet := []rune{
			'关', '關', '系', '係', '装', '訂', '計', '数',
			'漢', '字', '𠀾', '𠀀', '界',
			'あ', 'い', 'う', 'カ', 'ナ',
			'한', '글', '만',
			'a', 'Z', '0', '_', '-', ' ', '.', '/', '\n',
		}
		for i := 0; i < 300; i++ {
			var text strings.Builder
			n := rng.Intn(80)
			for j := 0; j < n; j++ {
				text.WriteRune(alphabet[rng.Intn(len(alphabet))])
			}

			input := text.String()
			got := collectCJKIndexKeys492(input)
			want := legacyCJKKeys(input)
			assertSameKeySet492(t, input, got, want)
		}
	})
}
