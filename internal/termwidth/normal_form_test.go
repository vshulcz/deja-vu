package termwidth

import "testing"

// The same name, typed two ways, has to measure the same: macOS hands paths
// back decomposed, and project names come from paths, so a column padded from
// the raw count starts a column early for half the machines deja runs on
// (#1824). The decomposed spellings are written as escapes on purpose — an
// editor that normalises the file would otherwise quietly delete the case.
func TestTheSameNameMeasuresTheSameInEitherNormalForm(t *testing.T) {
	const acute, diaeresis = "\u0301", "\u0308"
	for _, pair := range []struct{ decomposed, precomposed string }{
		{"e" + acute + "combining", "\u00e9combining"},
		{"u" + diaeresis + "ber-server", "\u00fcber-server"},
		{"nai" + diaeresis + "ve", "na\u00efve"},
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

// CutRight used to start a line with a mark whose base it had just cut away —
// a Greek name came back as a lone acute followed by "λφα" — and that mark
// draws on whatever precedes it, which on these screens is the ellipsis.
// Composing first makes a mark and its base one rune before anything is
// counted.
func TestACutNeverStartsWithAnOrphanedMark(t *testing.T) {
	// Built from escapes: a literal here has been normalised by an editor
	// twice already, and a composed literal deletes the case silently.
	const acute, diaeresis, breve = "\u0301", "\u0308", "\u0306"
	for _, s := range []string{
		"\u03b1" + acute + "\u03bb\u03c6\u03b1",             // alpha with tonos, decomposed
		"u" + diaeresis + "ber-server",                      // u with diaeresis, decomposed
		"\u0438" + breve + "\u043d\u0434\u0435\u043a\u0441", // Cyrillic short i, decomposed
	} {
		for width := 1; width <= 6; width++ {
			tail := CutRight(s, width)
			if tail == "" {
				continue
			}
			if r := []rune(tail)[0]; r >= 0x0300 && r <= 0x036F {
				t.Errorf("CutRight(%q, %d) starts with a combining mark: %q", s, width, tail)
			}
		}
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
