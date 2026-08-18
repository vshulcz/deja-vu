package cjkfold

import "testing"

func TestIsCJKAndCountCJK(t *testing.T) {
	for _, r := range []rune{'漢', 'ひ', 'カ', '한'} {
		if !IsCJK(r) {
			t.Errorf("%q is one of the scripts this package folds", r)
		}
	}
	for _, r := range []rune{'a', 'Я', '1', ' ', '。'} {
		if IsCJK(r) {
			t.Errorf("%q is not Han, kana or Hangul", r)
		}
	}
	// The count is what a caller needs to tell prose in these scripts from a
	// dump that merely contains some of it.
	if got := CountCJK("路径 is /tmp/数据库"); got != 5 {
		t.Errorf("CountCJK = %d, want 5", got)
	}
	if got := CountCJK("plain ascii only"); got != 0 {
		t.Errorf("CountCJK = %d on ASCII, want 0", got)
	}
}
