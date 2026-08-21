package main

import "testing"

// haystackSpread counts sessions, not mentions: a word repeated ten times inside
// one session still identifies that session, while a word every session says
// identifies nothing. Getting this backwards would mark common words as rare.
func TestHaystackSpreadCountsSessionsNotMentions(t *testing.T) {
	q := lmeQuestion{HaystackSessions: [][]lmeTurn{
		{{Content: "fujifilm bought"}, {Content: "camera talk about fujifilm again"}},
		{{Content: "camera talk again"}},
		{{Content: "camera notes"}},
	}}
	spread := haystackSpread(q)
	if got := spread["fujifilm"]; got != 1 {
		t.Errorf("fujifilm appears in one session, counted %d", got)
	}
	if got := spread["camera"]; got != 3 {
		t.Errorf("camera appears in three sessions, counted %d", got)
	}
}
