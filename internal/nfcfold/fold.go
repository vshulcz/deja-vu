// Package nfcfold composes NFD base+mark sequences (Latin, Greek, Cyrillic)
// back to their NFC
// precomposed form, so an accented word typed in one normalization matches the
// same word stored in the other (#1098).
package nfcfold

// hasCombining reports whether s carries a combining diacritical mark, the only
// case Compose can change. A string without one is returned untouched, so the
// overwhelming common case pays one scan and no allocation.
func hasCombining(s string) bool {
	for _, r := range s {
		// One test guards both ranges, so ordinary text pays a single
		// comparison per rune: the marks all sit above U+0300, and Arabic's
		// hamza and madda sit just past the block the other scripts use
		// (#1913).
		if r >= 0x0300 && (r <= 0x036F || (r >= 0x0653 && r <= 0x0655)) {
			return true
		}
	}
	return false
}

// Compose folds base+mark runs to their precomposed rune. It is idempotent on
// already-composed (NFC) text and repairs decomposed (NFD) text; both then hash
// to the same token. Multiple marks compose left to right, since the folded
// rune becomes the base for the next.
func Compose(s string) string {
	if !hasCombining(s) {
		return s
	}
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		if len(out) > 0 {
			if c, ok := compose[[2]rune{out[len(out)-1], r}]; ok {
				out[len(out)-1] = c
				continue
			}
		}
		out = append(out, r)
	}
	return string(out)
}
