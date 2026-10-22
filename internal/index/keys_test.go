package index

import (
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The tokenizer as it was before the keyer (#5567), kept as the oracle the
// keyer has to agree with key for key.

func legacyTokens(s string) []string {
	// Fold NFD to NFC so an accented word keys the same whether it was typed or
	// stored decomposed. A combining mark is category Mn, not a letter, so the
	// tokenizer below would otherwise split "café" (NFD) into "cafe" and drop
	// the accent, keying it apart from the precomposed "café" (#1098). Both the
	// stored text (indexKeys) and the query (queryKeys) pass through here, so
	// the fold is symmetric and the two sides always meet on one key.
	s = nfcfold.Compose(s)
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() >= 2 {
			t := b.String()
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		b.Reset()
	}
	for _, r := range query.Lower(s) {
		// A combining mark continues the word it sits on. Latin NFD is already
		// composed away above, but Arabic harakat, Hebrew niqqud, Thai vowel
		// signs and the Indic matras have no precomposed form, so the mark
		// ended the token: كَتَبَ was indexed as three one-letter tokens and
		// हिन्दी as two, and the words themselves were not in the index at all
		// (#1941). A mark cannot start a token, so a stray one still
		// separates.
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' ||
			(b.Len() > 0 && isMark(r)) {
			b.WriteRune(r)
			if b.Len() > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flush()
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func legacyIdentifierParts(s string) []string {
	var out []string
	var word []rune
	flushWord := func() {
		if len(word) >= 6 {
			legacySplitCompound(word, &out)
		}
		word = word[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			word = append(word, r)
			if len(word) > 64 {
				flushWord()
			}
			continue
		}
		flushWord()
	}
	flushWord()
	return out
}

func legacySplitCompound(word []rune, out *[]string) {
	start := 0
	boundaries := 0
	emit := func(end int) {
		if end-start >= 3 {
			*out = append(*out, query.Lower(string(word[start:end])))
		}
		start = end
	}
	for i := 1; i < len(word); i++ {
		c, p := word[i], word[i-1]
		switch {
		case c == '_' || c == '-':
			emit(i)
			start = i + 1
			boundaries++
		case unicode.IsUpper(c) && (unicode.IsLower(p) || unicode.IsDigit(p)):
			// getUser | getUserById: hump boundary
			emit(i)
			boundaries++
		case unicode.IsLower(c) && unicode.IsUpper(p) && i-1 > start:
			// JSONData -> JSON | Data: break before the last upper
			emit(i - 1)
			boundaries++
		}
	}
	if boundaries > 0 {
		emit(len(word))
	}
}

func legacyCJKIndexKeys(s string, emit func(tok string)) {
	// A CJK rune is never a single UTF-8 byte, so this guard preserves the
	// cjkBigrams fast path without decoding an ASCII-only message.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return
	}

	seen := map[uint64]struct{}{}
	emitFolded := func(r1, r2 rune) {
		// Grammar earns no posting. A pair of function runes — 的了, 在哪,
		// 什么 — is the CJK counterpart of a stop word: the query side has
		// dropped it from the term list since it was written, so the postings
		// were carrying the most frequent pairs in the language for nothing
		// (#492). queryKeys drops them from the AND for the same reason.
		if r1 != 0 && query.CJKFunctionRune(r1) && query.CJKFunctionRune(r2) {
			return
		}
		key := (uint64(r1) << 21) | uint64(r2)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		n := 1 + utf8.RuneLen(r2)
		if r1 != 0 {
			n += utf8.RuneLen(r1)
		}
		tok := make([]byte, n)
		tok[0] = 't'
		pos := 1
		if r1 != 0 {
			pos += utf8.EncodeRune(tok[pos:], r1)
		}
		utf8.EncodeRune(tok[pos:], r2)
		emit(string(tok))
	}

	var previous rune
	runLength := 0
	flush := func() {
		if runLength == 1 {
			// Zero is only the packed-key marker for a unigram. It is never
			// written into the emitted token.
			emitFolded(0, previous)
		}
		runLength = 0
	}

	for _, r := range s {
		if !cjkfold.Unspaced(r) {
			flush()
			continue
		}

		r = cjkfold.Rune(r)
		if runLength == 0 {
			previous = r
			runLength = 1
			continue
		}

		emitFolded(previous, r)
		previous = r
		runLength = 2
	}
	flush()
}

func legacyTextKeys(text string) []string {
	var out []string
	for _, tok := range legacyTokens(text) {
		out = append(out, "t"+tok)
	}
	for _, part := range legacyIdentifierParts(text) {
		out = append(out, "t"+part)
	}
	legacyCJKIndexKeys(text, func(tok string) { out = append(out, tok) })
	return out
}

// keyerTexts covers what the tokenizer branches on: case, ё, NFD and marks
// with no precomposed form, variation selectors, identifiers, words cut at 64
// bytes, CJK runs, fullwidth and halfwidth forms, and bytes that are not UTF-8.
func keyerTexts() []string {
	texts := []string{
		"",
		"a",
		"Go build FAILED: undefined: zorbleFrobnicate in getUserProfileById",
		"refresh_token_rotation JSONDataParser HTTPServer2Go x-y-z_abc",
		"Зелёный ЁлкаПраздник café café Ελληνικά",
		"كَتَبَ हिन्दी ไทยภาษา",
		"widget️ widget\U000E0100 x́",
		strings.Repeat("abcdefghij", 9) + " " + strings.Repeat("Ab", 40) + " " + strings.Repeat("ё", 40),
		strings.Repeat("я", 32) + "́́" + strings.Repeat("ж", 3),
		"韓國簽證 韩国签证 这是什么问题 中国个人 日本語のテスト 한국어 텍스트",
		"ｰﾞﾟ ＡＢＣ ｱｲｳ 中a国 𠀀𠀁 中",
		"bad \xff\xfe bytes\xc3 mid\xe4\xb8word ok",
		"--- FAIL: TestFoo (0.00s)\n\tfoo_test.go:12: got 1, want 2\nexit status 1",
		"İstanbul ǅemal ẞtraße ΣΊΣΥΦΟΣ",
	}
	r := rand.New(rand.NewSource(5567))
	alphabet := []string{"a", "Z", "_", "-", " ", "ё", "Ё", "é", "́", "中", "國", "的", "了", "ｱ", "\xff", "1", "️", "ب", "َ", "\n", "Σ", "ß"}
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		for n := r.Intn(120); n > 0; n-- {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		texts = append(texts, b.String())
	}
	return texts
}

// The keyer is the tokenizer written to reuse its buffers; it must cut the
// same words, parts and bigrams the string version did, and tokens must come
// out in the same order, since the cooccur sidecar interns them in it.
func TestKeyerEmitsTheKeysTheStringTokenizerDid(t *testing.T) {
	for _, text := range keyerTexts() {
		if got, want := tokens(text), legacyTokens(text); !slices.Equal(got, want) {
			t.Fatalf("tokens(%q) = %q, want %q", text, got, want)
		}
		if got, want := identifierParts(text), legacyIdentifierParts(text); !slices.Equal(got, want) {
			t.Fatalf("identifierParts(%q) = %q, want %q", text, got, want)
		}
		var gotCJK, wantCJK []string
		cjkIndexKeys(text, func(tok string) { gotCJK = append(gotCJK, tok) })
		legacyCJKIndexKeys(text, func(tok string) { wantCJK = append(wantCJK, tok) })
		if !slices.Equal(gotCJK, wantCJK) {
			t.Fatalf("cjkIndexKeys(%q) = %q, want %q", text, gotCJK, wantCJK)
		}
		got, want := distinctSorted(indexKeys(text)), distinctSorted(legacyTextKeys(text))
		if !slices.Equal(got, want) {
			t.Fatalf("indexKeys(%q) = %q, want %q", text, got, want)
		}
	}
}

// One keyer keys a whole worker's share of the store, so nothing a message
// leaves in its buffers may reach the next one's keys.
func TestAKeyerCarriesNothingFromOneMessageToTheNext(t *testing.T) {
	var k keyer
	months := []time.Time{
		time.Date(2026, 5, 31, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 1, 0, 0, 0, time.FixedZone("x", 3*3600)),
		{},
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	for i, text := range keyerTexts() {
		when := months[i%len(months)]
		var got []string
		k.keys(text, when, func(b []byte) { got = append(got, string(b)) })
		want := legacyTextKeys(text)
		want = append(want, dateTokens(when)...)
		if g, w := distinctSorted(got), distinctSorted(want); !slices.Equal(g, w) {
			t.Fatalf("keys(%q, %v) = %q, want %q", text, when, g, w)
		}
	}
}

func distinctSorted(keys []string) []string {
	out := slices.Clone(keys)
	sort.Strings(out)
	return slices.Compact(out)
}

// Keying a message used to allocate a string per word, a builder per word, a
// lowered copy of the text and a map: on a 12k-session store the workers spent
// a fifth of their CPU on the heap lock that garbage takes (#5567). A word the
// batch has already seen costs nothing now.
func TestSpillWorkersAllocateNothingPerRepeatedWord(t *testing.T) {
	var jobs []tokenJob
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := 0; i < 20000; i++ {
		text := fmt.Sprintf("The build FAILED on runSpillWorker: undefined zorbleFrobnicate in package main, step %d of the deploy pipeline 日本語のテスト", i%50)
		jobs = append(jobs, tokenJob{text: text, offset: off, sid: uint32(i/40 + 1), when: when})
		off += int64(len(text))
	}
	sp, err := newSpiller(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	// About twenty keys a message; the bound leaves room for the batches, the
	// spill buffers and a copy of each key per batch.
	per := float64(after.Mallocs-before.Mallocs) / float64(len(jobs))
	t.Logf("%.2f allocations per message", per)
	if per > 2 {
		t.Fatalf("%.1f allocations per message keyed", per)
	}
}
