package main

import (
	"testing"
)

// The card is an SVG document. Writing it into card.png produced a file
// GitHub serves as a PNG and browsers refuse, while the command printed the
// markdown to embed it — a broken image for anyone who followed the hint.

func TestCardFileNameKeepsTheFormatHonest(t *testing.T) {
	for in, want := range map[string]string{
		"deja-stats.svg": "deja-stats.svg",
		"CARD.SVG":       "CARD.SVG",
		"card.png":       "card.png.svg",
		"card.txt":       "card.txt.svg",
		"card":           "card.svg",
		"dir/card":       "dir/card.svg",
	} {
		if got := cardFileName(in); got != want {
			t.Fatalf("cardFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
