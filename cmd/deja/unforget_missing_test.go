package main

import (
	"strings"
	"testing"
)

// Lifting a tombstone restores nothing in two different situations, and they
// have different answers: a session deja only ever held in its own index came
// from another machine and `sync import` brings it back, while a transcript
// this machine wrote and then deleted is simply not on disk — pointing the
// second at sync import blames a machine the user never had (#1755).
func TestUnforgetSaysWhyNothingCameBack(t *testing.T) {
	for _, tc := range []struct {
		keys []string
		want []string
		not  []string
	}{
		{
			keys: []string{"claude:imported-ab12cd34"},
			want: []string{"another machine", "sync import"},
			not:  []string{"no longer on this machine"},
		},
		{
			keys: []string{"claude:ts-01"},
			want: []string{"no longer on this machine"},
			not:  []string{"another machine", "sync import"},
		},
		{
			keys: []string{"claude:imported-ab12cd34", "claude:ts-01"},
			want: []string{"another machine", "no longer on this machine"},
		},
	} {
		got := unforgetGoneLines(tc.keys)
		joined := strings.Join(got, "\n")
		for _, want := range tc.want {
			if !strings.Contains(joined, want) {
				t.Errorf("%v said %q, missing %q", tc.keys, joined, want)
			}
		}
		for _, no := range tc.not {
			if strings.Contains(joined, no) {
				t.Errorf("%v said %q, which should not mention %q", tc.keys, joined, no)
			}
		}
	}
	if got := unforgetGoneLines(nil); len(got) != 0 {
		t.Errorf("nothing missing still said %q", got)
	}

	// One key and several read as sentences, not as a count with a plural
	// bolted on.
	one := strings.Join(unforgetGoneLines([]string{"claude:ts-01"}), " ")
	many := strings.Join(unforgetGoneLines([]string{"claude:ts-01", "claude:ts-02"}), " ")
	if !strings.Contains(one, "claude:ts-01 is no longer") || !strings.Contains(one, "the tombstone is lifted") {
		t.Errorf("one key reads as %q", one)
	}
	if !strings.Contains(many, "claude:ts-01, claude:ts-02 are no longer") || !strings.Contains(many, "the tombstones are lifted") {
		t.Errorf("two keys read as %q", many)
	}
}
