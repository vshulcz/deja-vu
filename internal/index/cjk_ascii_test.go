package index

import (
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"strings"
	"testing"
)

// indexKeys calls cjkBigrams on every message body it sees, most of which are
// diffs, paths, stack traces and English prose with no CJK rune anywhere. This
// table pins the contract that lets those exit early: what comes back for a
// string with no CJK run is nothing, whatever else the bytes happen to be.
//
// It is also the guard on the byte scan itself. A scan that decided "ASCII" too
// eagerly would return nil for the CJK rows below; one that never fired would
// leave the ASCII rows unchanged and quietly cost what it always cost. Only the
// first failure mode can corrupt an index, and these rows catch it.
func TestCJKBigramsOnlyFiresOnCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"ascii prose", "the scheduler times out under load", nil},
		{"ascii symbols and digits", "GOFLAGS=-mod=mod exit(1) 200 OK", nil},
		{"cyrillic is not CJK", "очередь переполнена", nil},
		{"latin with accents", "café naïve", nil},
		{"invalid utf-8", "\xff\xfe\xfd", nil},
		{"single han keeps its unigram", "茶", []string{"茶"}},
		{"han run", "装订计数", []string{"装订", "订计", "计数"}},
		{"han among ascii", "abc装订def", []string{"装订"}},
		{"runs split by ascii", "装订 abc 计数", []string{"装订", "计数"}},
		{"repeated bigram appears once", "装订装订", []string{"装订", "订装"}},
		{"hiragana", "こんにちは", []string{"こん", "んに", "にち", "ちは"}},
		{"hangul", "한국어", []string{"한국", "국어"}},
		{"cyrillic next to han", "очередь装订", []string{"装订"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cjkfold.Bigrams(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("cjkfold.Bigrams(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("cjkfold.Bigrams(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// A body with no CJK in it should not allocate: nothing is built, so nothing
// needs a heap. This is the part a correctness table cannot see — the table
// passed before the early exit existed too, because the answer was always nil;
// what changed is the work done to arrive at it.
func TestCJKBigramsDoesNotAllocateOnNonCJK(t *testing.T) {
	body := strings.Repeat("the ingest worker leaks goroutines after a reload. ", 200)
	if allocs := testing.AllocsPerRun(50, func() { cjkfold.Bigrams(body) }); allocs != 0 {
		t.Errorf("cjkBigrams on a %d-byte ASCII body made %v allocations, want 0", len(body), allocs)
	}
}

func BenchmarkCJKBigramsASCII(b *testing.B) {
	body := strings.Repeat("the ingest worker leaks goroutines after a reload. ", 200)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cjkfold.Bigrams(body)
	}
}

func BenchmarkCJKBigramsCJK(b *testing.B) {
	body := strings.Repeat("摄取工作进程在重载之后泄漏协程。", 100)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cjkfold.Bigrams(body)
	}
}
