package index

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// bruteCloseTokens is the definition closeTokens must not drift from: every
// token in the corpus, compared directly.
func bruteCloseTokens(query string, catalog map[string]bool) []string {
	type match struct {
		token string
		d     int
	}
	limit := 1
	if len([]rune(query)) >= 8 {
		limit = 2
	}
	var ms []match
	for token := range catalog {
		if d := damerauDistance(query, token, limit); d <= limit {
			ms = append(ms, match{token, d})
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].d == ms[j].d {
			return ms[i].token < ms[j].token
		}
		return ms[i].d < ms[j].d
	})
	if len(ms) > 8 {
		ms = ms[:8]
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.token
	}
	return out
}

// Bucketing by length must not lose a match. Only tokens within the edit
// limit of the query's length can match, but the bookkeeping — rune vs byte
// length, the overflow bucket for very long tokens — is where it would go
// wrong quietly.
func TestFuzzyLengthBucketsMatchBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	al := []rune("abcdefghijklmnopqrstuvwxyz")
	cyr := []rune("абвгдеёжзийклмнопрстуфхцчшщыэюя")
	catalog := map[string]bool{}
	word := func(alpha []rune, n int) string {
		b := make([]rune, n)
		for i := range b {
			b[i] = alpha[rng.Intn(len(alpha))]
		}
		return string(b)
	}
	for i := 0; i < 4000; i++ {
		catalog[word(al, 1+rng.Intn(30))] = true
		catalog[word(cyr, 1+rng.Intn(20))] = true
	}
	// Tokens past the bucket cap must still be reachable.
	long := word(al, maxIndexedTokenLen+8)
	catalog[long] = true
	catalog[long[:len(long)-1]] = true
	catalog[word(cyr, maxIndexedTokenLen+3)] = true

	idx := newTokenIndex(catalog)
	queries := []string{"a", "ab", "abcdefgh", "мосты", "конфигурация", long, long[:20], strings.Repeat("z", 40)}
	for i := 0; i < 40; i++ {
		queries = append(queries, word(al, 1+rng.Intn(20)), word(cyr, 1+rng.Intn(16)))
	}
	for _, q := range queries {
		want := bruteCloseTokens(q, catalog)
		got := closeTokens(q, idx)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("query %q (%d runes): bucketed=%v brute=%v", q, utf8.RuneCountInString(q), got, want)
		}
	}
}

// Every token must land in exactly one bucket, and the bucket must agree with
// the token's rune length.
func TestTokenIndexBucketsByRuneLength(t *testing.T) {
	catalog := map[string]bool{"ab": true, "abc": true, "мир": true, "конфигурация": true, strings.Repeat("x", maxIndexedTokenLen+5): true}
	idx := newTokenIndex(catalog)
	seen := map[string]int{}
	for n, bucket := range idx.byLen {
		for _, tok := range bucket {
			seen[tok]++
			want := utf8.RuneCountInString(tok)
			if want > maxIndexedTokenLen {
				want = maxIndexedTokenLen + 1
			}
			if n != want {
				t.Errorf("token %q sits in bucket %d, want %d", tok, n, want)
			}
		}
	}
	for tok := range catalog {
		if seen[tok] != 1 {
			t.Errorf("token %q appears in %d buckets", tok, seen[tok])
		}
	}
}
