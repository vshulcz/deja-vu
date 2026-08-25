package termwidth

import "testing"

// The same name, typed two ways, has to measure the same: macOS hands paths
// back decomposed, and project names come from paths, so a column padded from
// the raw count starts a column early for half the machines deja runs on
// (#1824). The decomposed spellings are written as escapes on purpose — an
// editor that normalises the file would otherwise quietly delete the case.
func TestTheSameNameMeasuresTheSameInEitherNormalForm(t *testing.T) {
	for _, pair := range []struct{ decomposed, precomposed string }{
		{"écombining", "écombining"},
		{"über-server", "über-server"},
		{"naïve", "naïve"},
	} {
		d, p := Columns(pair.decomposed), Columns(pair.precomposed)
		if d != p {
			t.Errorf("%q measures %d and %q measures %d", pair.decomposed, d, pair.precomposed, p)
		}
		if p != len([]rune(pair.precomposed)) {
			t.Errorf("the precomposed spelling itself measures %d for %d runes", p, len([]rune(pair.precomposed)))
		}
	}
}

// What stays over-counted, on purpose: a lone combining mark that composes with
// nothing, a zero-width space, and the CJK marks the wide table finds. Over-
// counting shortens a line; under-counting runs it past the edge.
func TestOverCountingStaysWhereItWasDeliberate(t *testing.T) {
	if got := Columns("a\u200bb"); got != 3 {
		t.Errorf("a zero-width space now measures differently: %d", got)
	}
	if got := Columns("が"); got != 4 {
		t.Errorf("the CJK combining mark now measures differently: %d", got)
	}
	if got := Columns("́"); got != 1 {
		t.Errorf("a combining mark that composes with nothing measures %d", got)
	}
}

// Ordinary text is untouched, which is what keeps the fast path honest.
func TestPlainTextIsUnchanged(t *testing.T) {
	for s, want := range map[string]int{
		"laptop":       6,
		"数据分片":         8,
		"🚀rocket":      8,
		"build-server": 12,
	} {
		if got := Columns(s); got != want {
			t.Errorf("%q measures %d, want %d", s, got, want)
		}
	}
}
