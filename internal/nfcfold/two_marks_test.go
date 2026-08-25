package nfcfold

import "testing"

// A letter carrying two marks composes in two steps, and the intermediate is a
// letter of its own: e + U+0323 is U+1EB9, which then takes U+0302 to become
// U+1EC7. The generator accepted a base only up to U+024F, so the second step
// had no entry and the fold stopped halfway — a Vietnamese word typed NFD, the
// form a macOS path hands back, matched nothing (#1872). Greek Extended is the
// same shape, and both scripts are named in this package's doc comment.
//
// Written as escapes: a decomposed literal typed into a Go file is normalised
// on the way in, which once made a test pass against unfixed code.
func TestComposeFoldsLettersCarryingTwoMarks(t *testing.T) {
	cases := []struct {
		name, nfd, nfc string
	}{
		{"vietnamese e circumflex dot below", "\u0065\u0323\u0302", "\u1ec7"},
		{"vietnamese o horn dot below", "\u006f\u031b\u0323", "\u1ee3"},
		{"vietnamese a breve dot below", "\u0061\u0323\u0306", "\u1eb7"},
		{"latin s dot below dot above", "\u0073\u0323\u0307", "\u1e69"},
		{"greek alpha psili oxia", "\u03b1\u0313\u0301", "\u1f04"},
		{"greek eta perispomeni ypogegrammeni", "\u03b7\u0342\u0345", "\u1fc7"},
		{"latin ezh caron", "\u0292\u030c", "\u01ef"},
	}
	for _, c := range cases {
		if c.nfd == c.nfc {
			t.Fatalf("%s: nfd and nfc are identical bytes — test is trivial", c.name)
		}
		if got := Compose(c.nfd); got != c.nfc {
			t.Errorf("%s: Compose(NFD %+q) = %+q, want NFC %+q", c.name, c.nfd, got, c.nfc)
		}
		if got := Compose(c.nfc); got != c.nfc {
			t.Errorf("%s: Compose(NFC) changed it to %+q", c.name, got)
		}
	}
}

// A standalone diacritic is not a letter, and the generator asked for one — so
// the three Greek marks written as a spacing diaeresis plus a combining mark
// folded to nothing, and a text quoting one matched only in the form it was
// typed (#1894). The table is about text, not about letters.
func TestComposeFoldsAStandaloneDiacritic(t *testing.T) {
	cases := []struct {
		name, nfd, nfc string
	}{
		{"greek dialytika tonos", "\u00a8\u0301", "\u0385"},
		{"greek dialytika perispomeni", "\u00a8\u0342", "\u1fc1"},
		{"greek dialytika varia", "\u00a8\u0300", "\u1fed"},
	}
	for _, c := range cases {
		if c.nfd == c.nfc {
			t.Fatalf("%s: nfd and nfc are identical bytes — test is trivial", c.name)
		}
		if got := Compose(c.nfd); got != c.nfc {
			t.Errorf("%s: Compose(NFD %+q) = %+q, want NFC %+q", c.name, c.nfd, got, c.nfc)
		}
	}
}

// Arabic writes hamza and madda as marks at U+0653–U+0655, just past the block
// the scan looks at, so an Arabic word typed decomposed folded to nothing and
// keyed apart from the same word stored composed (#1913). Eight pairs, none of
// them a composition exclusion.
func TestComposeFoldsArabicHamzaAndMadda(t *testing.T) {
	cases := []struct {
		name, nfd, nfc string
	}{
		{"alef with madda", "\u0627\u0653", "\u0622"},
		{"alef with hamza above", "\u0627\u0654", "\u0623"},
		{"waw with hamza", "\u0648\u0654", "\u0624"},
		{"alef with hamza below", "\u0627\u0655", "\u0625"},
		{"yeh with hamza", "\u064a\u0654", "\u0626"},
		{"heh with yeh above", "\u06d5\u0654", "\u06c0"},
		{"heh goal with hamza", "\u06c1\u0654", "\u06c2"},
		{"yeh barree with hamza", "\u06d2\u0654", "\u06d3"},
	}
	for _, c := range cases {
		if c.nfd == c.nfc {
			t.Fatalf("%s: nfd and nfc are identical bytes — test is trivial", c.name)
		}
		if got := Compose(c.nfd); got != c.nfc {
			t.Errorf("%s: Compose(NFD %+q) = %+q, want NFC %+q", c.name, c.nfd, got, c.nfc)
		}
		if got := Compose(c.nfc); got != c.nfc {
			t.Errorf("%s: Compose(NFC) changed it to %+q", c.name, got)
		}
	}
}
