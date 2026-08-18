package cjkfold

import (
	"reflect"
	"testing"
)

func TestBigrams(t *testing.T) {
	// Overlapping pairs inside a run, so a query for any adjacent pair reaches
	// the text that contains it.
	if got, want := Bigrams("装订计数"), []string{"装订", "订计", "计数"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Bigrams = %q, want %q", got, want)
	}
	// A run of one keeps its character: there is no pair to make.
	if got, want := Bigrams("我 them"), []string{"我"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Bigrams = %q, want %q", got, want)
	}
	// Runs never cross a non-CJK boundary, and repeats are emitted once.
	if got, want := Bigrams("装订 and 装订"), []string{"装订"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Bigrams = %q, want %q", got, want)
	}
	// ASCII-only text decodes nothing.
	if got := Bigrams("nothing to see here"); got != nil {
		t.Errorf("Bigrams = %q on ASCII, want none", got)
	}
	// Mixed scripts: each run is its own.
	if got, want := Bigrams("ログ and 日志"), []string{"ログ", "日志"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Bigrams = %q, want %q", got, want)
	}
}
