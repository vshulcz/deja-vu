package redact

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// entropySpans replaced a regexp. A rewrite of a matcher that decides what gets
// redacted is exactly the change that must be proved rather than reviewed, so
// this checks it against the pattern it replaced on inputs designed to hit the
// boundaries, and then on a lot of random ones.
var referenceRE = regexp.MustCompile(`[A-Za-z0-9+/_-]{20,}={0,2}`)

func reference(s string) [][2]int {
	var out [][2]int
	for _, m := range referenceRE.FindAllStringIndex(s, -1) {
		out = append(out, [2]int{m[0], m[1]})
	}
	return out
}

func TestEntropySpansMatchesThePatternItReplaced(t *testing.T) {
	cases := []string{
		"",
		"short",
		strings.Repeat("a", 19),         // one below the threshold
		strings.Repeat("a", 20),         // exactly the threshold
		strings.Repeat("a", 20) + "=",   // one pad
		strings.Repeat("a", 20) + "==",  // two pads
		strings.Repeat("a", 20) + "===", // a third is not part of it
		strings.Repeat("a", 20) + "=" + strings.Repeat("b", 25),
		"prefix " + strings.Repeat("x", 30) + " suffix",
		strings.Repeat("a", 19) + " " + strings.Repeat("b", 21),
		"a+b/c_d-e" + strings.Repeat("Z", 25),
		"кириллица " + strings.Repeat("q", 22) + " ещё",
		strings.Repeat("=", 5),
		strings.Repeat("a", 25) + "==" + strings.Repeat("b", 25) + "=",
	}
	for _, s := range cases {
		if got, want := entropySpans(s), reference(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("input %q:\n got %v\nwant %v", s, got, want)
		}
	}
}

func TestEntropySpansMatchesOnRandomInput(t *testing.T) {
	// A fixed seed: a matcher test that fails only sometimes is worse than no
	// test, because nobody can reproduce the failure.
	rng := rand.New(rand.NewSource(20260729))
	alphabet := []byte("abcXYZ019+/_-= \n\t.:\"'{}")
	for i := 0; i < 4000; i++ {
		n := rng.Intn(120)
		b := make([]byte, n)
		for j := range b {
			b[j] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(b)
		if got, want := entropySpans(s), reference(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("input %q:\n got %v\nwant %v", s, got, want)
		}
	}
}

// The scan is only worth having if it is faster than what it replaced.
func BenchmarkEntropySpans(b *testing.B) {
	s := hashy
	b.SetBytes(int64(len(s)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = entropySpans(s)
	}
}

func BenchmarkEntropySpansRegexp(b *testing.B) {
	s := hashy
	b.SetBytes(int64(len(s)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = referenceRE.FindAllStringIndex(s, -1)
	}
}
