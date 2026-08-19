package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The floor is what keeps a preamble from swallowing the line behind it, and
// it is documented in characters. Counted in bytes it was forty characters for
// English and twenty for Russian, so the same shape of text lost its answer in
// one language and kept it in the other.
func TestSameFactFloorCountsCharacters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opening string
		full    string
		same    bool
	}{
		{
			"english preamble is short enough to keep the answer",
			"we looked at a couple of options here",
			"we looked at a couple of options here and picked the token bucket",
			false,
		},
		{
			"russian preamble of the same length keeps it too",
			"мы обсудили несколько вариантов",
			"мы обсудили несколько вариантов и выбрали токен-бакет",
			false,
		},
		{
			"chinese preamble keeps it too",
			"我们讨论了几种方案还比较了两个库",
			"我们讨论了几种方案还比较了两个库最后选了令牌桶限流",
			false,
		},
		{
			"a real trimmed conclusion is still one fact",
			"the retry queue needs jitter or every worker wakes at the same moment",
			"the retry queue needs jitter or every worker wakes at the same moment, so we spread them over a second",
			true,
		},
		{
			"and in Russian too, once it is long enough to be a fact",
			"очередь повторов требует джиттера, иначе воркеры просыпаются вместе",
			"очередь повторов требует джиттера, иначе воркеры просыпаются вместе, поэтому размазали по секунде",
			true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The fixture has to sit where the two counts disagree, or it
			// passes whichever way the floor is measured.
			chars, bytes := utf8.RuneCountInString(tc.opening), len(tc.opening)
			t.Logf("opening: %d characters, %d bytes", chars, bytes)
			if got := sameFact(tc.full, tc.opening); got != tc.same {
				t.Errorf("sameFact(%q, %q) = %v, want %v", tc.full, tc.opening, got, tc.same)
			}
			// Symmetric: which argument is longer must not matter.
			if got := sameFact(tc.opening, tc.full); got != tc.same {
				t.Errorf("sameFact reversed = %v, want %v", got, tc.same)
			}
		})
	}
}

// The consequence in the payload: the conclusion behind a preamble survives.
func TestRecallKeepsTheAnswerBehindANonLatinPreamble(t *testing.T) {
	const opening = "мы обсудили несколько вариантов"
	const full = "мы обсудили несколько вариантов и выбрали токен-бакет с окном в секунду"
	got := withoutShownAnswer([]string{full}, []string{"→ " + opening})
	if len(got) != 1 {
		t.Fatalf("the conclusion was dropped as a duplicate of its own preamble: %q", got)
	}
	if !strings.Contains(got[0], "токен-бакет") {
		t.Errorf("wrong line survived: %q", got[0])
	}

	// And the case the dropping exists for still works: a line the excerpt
	// already carries whole is not repeated.
	const answer = "очередь повторов требует джиттера, иначе воркеры просыпаются вместе"
	if got := withoutShownAnswer([]string{answer}, []string{"→ " + answer}); len(got) != 0 {
		t.Errorf("a conclusion the excerpt already shows was repeated: %q", got)
	}
}
