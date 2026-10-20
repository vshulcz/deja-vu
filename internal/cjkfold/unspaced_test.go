package cjkfold

import "testing"

// Below U+0E00 nothing is unspaced, so a Latin rune is answered without the
// script tables; Thai and the CJK scripts above it still are (#5467).
func TestUnspacedAcrossTheShortcut(t *testing.T) {
	for _, c := range []struct {
		r    rune
		want bool
	}{
		{'a', false}, {'é', false}, {'ж', false}, {0x0DFF, false},
		{'ก', true}, {'๙', true}, {0x0E80, false},
		{'中', true}, {'あ', true}, {'ー', true}, {'한', true},
	} {
		if got := Unspaced(c.r); got != c.want {
			t.Errorf("Unspaced(%U) = %v, want %v", c.r, got, c.want)
		}
	}
}
