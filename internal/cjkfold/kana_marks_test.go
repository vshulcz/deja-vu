package cjkfold

import (
	"reflect"
	"testing"
)

// ー is not decoration: it appears in most Japanese loanwords, and Unicode
// files it under Common rather than under Katakana. Leaving it out of the
// script test split every such word into two runs (#1319).
func TestTheProlongedSoundMarkIsPartOfTheWord(t *testing.T) {
	for _, tc := range []struct {
		word string
		want []string
	}{
		{"ステージング", []string{"ステ", "テー", "ージ", "ジン", "ング"}},
		{"サーバー", []string{"サー", "ーバ", "バー"}},
		{"リトライ", []string{"リト", "トラ", "ライ"}},
	} {
		if got := Bigrams(tc.word); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: bigrams = %q, want %q", tc.word, got, tc.want)
		}
	}
}

// The iteration marks look like the same case and are not: Go's own tables
// already file them under Han and the kana scripts. Pinned so a future change
// there is visible here rather than as a Japanese search that stops matching.
func TestIterationMarksStayInsideTheirWord(t *testing.T) {
	if got := Bigrams("人々"); len(got) != 1 || got[0] != "人々" {
		t.Errorf("人々 gave %q, want one bigram", got)
	}
	// Each mark on its own: two of these are ours and six come from the script
	// tables, and this says nothing about which is which — only that a word
	// containing any of them stays one run.
	for _, r := range []rune{'々', 'ー', 'ヽ', 'ヾ', 'ゝ', 'ゞ', '〻', '\uFF70'} {
		if !IsCJK(r) {
			t.Errorf("%q is written inside a word and is not counted as part of it", r)
		}
	}
}

// And nothing else moved: a mark alone is still not a word, and Latin,
// Cyrillic and punctuation are untouched.
func TestOnlyTheMarksMoved(t *testing.T) {
	for _, r := range []rune{'a', 'Я', '5', '-', '_', ' ', '。', '、'} {
		if IsCJK(r) {
			t.Errorf("%q counts as CJK", r)
		}
	}
	if got := Bigrams("ー"); len(got) != 0 {
		t.Errorf("a lone mark produced %q", got)
	}
	if got := Bigrams("retry queue"); len(got) != 0 {
		t.Errorf("ASCII produced %q", got)
	}
}

// A rule drawn with long dashes is not Japanese prose: CountCJK feeds the
// decision, and counting a mark with no word beside it made a table border read
// as a sentence.
func TestMarksAloneAreNotProse(t *testing.T) {
	if got := CountCJK("ーーーー"); got != 0 {
		t.Errorf("a line of dashes counted %d CJK runes", got)
	}
	if got := CountCJK("ステージング"); got != 6 {
		t.Errorf("a Japanese word counted %d of 6", got)
	}
	if got := CountCJK("サーバー"); got != 4 {
		t.Errorf("a word with two marks counted %d of 4", got)
	}
}

// The halfwidth voicing marks are written after the kana they voice, so a word
// like ｻｰﾊﾞｰ is one run — they are unclaimed by every script table, the same
// hole the prolonged mark was in.
func TestHalfwidthVoicingMarksStayInsideTheWord(t *testing.T) {
	if got := Bigrams("ｻｰﾊﾞｰ"); len(got) == 0 {
		t.Fatal("a halfwidth word produced no bigrams")
	} else if len(got) != 4 {
		t.Errorf("ｻｰﾊﾞｰ gave %q, want four overlapping pairs", got)
	}
}
