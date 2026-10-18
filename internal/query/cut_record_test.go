package query

import (
	"testing"
	"time"
)

func TestCutRecordKeepsOutUpToTheCut(t *testing.T) {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	o := Options{ExcludeUpTo: map[string]time.Time{"a": at}}
	for _, c := range []struct {
		id   string
		t    time.Time
		want bool
	}{
		{"a", at.Add(-time.Second), true},
		{"a", at, true},
		{"a", time.Time{}, true},
		{"a", at.Add(time.Second), false},
		{"b", at, false},
	} {
		if got := o.CutRecord(c.id, c.t); got != c.want {
			t.Errorf("CutRecord(%q, %v) = %v, want %v", c.id, c.t, got, c.want)
		}
	}
}
