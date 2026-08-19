package digest

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// firstSentences is what makes a long conclusion fit the recall budget: it
// states itself up front, so the opening sentence is kept and the explanation
// is dropped. Chinese and Japanese end a sentence with 。, not with a period
// and a space, so the function found no sentence at all and handed the whole
// message back — which then did not fit and was dropped whole.
func TestFirstSentencesFindsCJKSentenceEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		n     int
		want  string
		short bool // the result must be shorter than the input
	}{
		{
			"chinese",
			"重试队列在预发环境卡住了。所有工作进程同时醒来，所以我们把它们分散到一秒内。抖动的上限是轮询间隔。",
			1,
			"重试队列在预发环境卡住了。",
			true,
		},
		{
			"japanese",
			"リトライキューがステージングで詰まりました。ワーカーが同時に起きるので、一秒の範囲に分散させました。",
			1,
			"リトライキューがステージングで詰まりました。",
			true,
		},
		{
			"chinese question",
			"为什么重试预算在两次尝试之间被重置？我们查了上传队列的代码。",
			1,
			"为什么重试预算在两次尝试之间被重置？",
			true,
		},
		{
			"fullwidth exclamation",
			"部署已经回滚了！后面的改动都不需要了。",
			1,
			"部署已经回滚了！",
			true,
		},
		{
			"two sentences",
			"重试队列在预发环境卡住了。所有工作进程同时醒来。抖动的上限是轮询间隔。",
			2,
			"重试队列在预发环境卡住了。所有工作进程同时醒来。",
			true,
		},
		// English and Russian are untouched: the ASCII rule, space and all.
		{
			"english",
			"The retry queue stalls on staging. Every worker wakes at the same moment.",
			1,
			"The retry queue stalls on staging.",
			true,
		},
		{
			"russian",
			"Очередь повторов зависает на предпродакшене. Все воркеры просыпаются разом.",
			1,
			"Очередь повторов зависает на предпродакшене.",
			true,
		},
		// The space rule still protects a version number from reading as an end.
		{
			"version number",
			"We pinned it to v1.2.3 and the flapping stopped. Then we moved on.",
			1,
			"We pinned it to v1.2.3 and the flapping stopped.",
			true,
		},
		// And the fullwidth full stop is not a sentence end, because it is
		// also the decimal point in fullwidth digits — where the space rule
		// cannot protect it.
		{
			"fullwidth decimal",
			"\uff11\uff0e\uff12\u7248\u672c\u5df2\u7ecf\u53d1\u5e03\u4e86\u3002\u56de\u6eda\u4e0d\u9700\u8981\u4e86\u3002",
			1,
			"\uff11\uff0e\uff12\u7248\u672c\u5df2\u7ecf\u53d1\u5e03\u4e86\u3002",
			true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := firstSentences(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("firstSentences(%q, %d) =\n  %q\nwant\n  %q", tc.in, tc.n, got, tc.want)
			}
			if tc.short && utf8.RuneCountInString(got) >= utf8.RuneCountInString(tc.in) {
				t.Errorf("nothing was trimmed: %d characters in, %d out",
					utf8.RuneCountInString(tc.in), utf8.RuneCountInString(got))
			}
			// Whatever comes back is a prefix of the input, whole characters
			// only: a cut through a three-byte stop would be invalid UTF-8.
			if !strings.HasPrefix(tc.in, got) {
				t.Errorf("the result is not a prefix of the input: %q", got)
			}
			if !utf8.ValidString(got) {
				t.Errorf("invalid UTF-8: %q", got)
			}
		})
	}
}

// A message with no sentence end at all still falls back to the byte cap, and
// the cap cuts whole characters.
func TestFirstSentencesFallsBackWithoutAnEnd(t *testing.T) {
	long := strings.Repeat("重试队列在预发环境卡住了", 40) // no stop anywhere
	got := firstSentences(long, 1)
	if !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8 from the fallback cut: %q", got)
	}
	if len(got) >= len(long) {
		t.Errorf("the fallback did not cut: %d bytes in, %d out", len(long), len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("the cut is not marked: %q", got)
	}
}
