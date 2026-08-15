package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/mark"
)

// The mark used to be a pasted path in four places: the SVG assets, the stats
// card, the stats page and the local viewer. Four copies of one animal, and
// editing the sprite reached none of them. They are all drawn from
// internal/mark now — these helpers are the shared markup, and
// scripts/genlogo does the same for the files on disk.

// markStill is the ready pose as plain fills, for the places that do not move:
// the viewer header and the stats card.
func markStill(x0, y0, size int) string {
	var b strings.Builder
	for _, p := range mark.Paths(mark.Ready, x0, y0, size, nil) {
		b.WriteString(`<path fill="` + p.Fill + `" d="` + p.D + `"/>`)
	}
	return b.String()
}

// markAlive is the same animal with the tail and the eyes in their own groups,
// for the stats page. The still layer keeps coat where the eyes go — cutting
// them out leaves two holes in the head each time the blink hides the open pair
// — while the tail is left out entirely, since it hangs clear of the body.
func markAlive(x0, y0, size int) string {
	moving := map[mark.Cell]bool{}
	for _, pose := range mark.WagCycle {
		for _, c := range mark.Tails[pose] {
			moving[c] = true
		}
	}
	still := mark.Ready
	still.EyeSet = "none"

	var b strings.Builder
	for _, p := range mark.Paths(still, x0, y0, size, func(c mark.Cell) bool { return moving[c] }) {
		b.WriteString(`<path fill="` + p.Fill + `" d="` + p.D + `"/>`)
	}
	seen := map[string]bool{}
	i := 0
	for _, pose := range mark.WagCycle {
		if seen[pose] {
			continue
		}
		seen[pose] = true
		b.WriteString(`<g class="t` + string(rune('0'+i)) + `"><path fill="` + mark.Hex[mark.Coat] +
			`" d="` + mark.CellsD(mark.Tails[pose], x0, y0, size) + `"/></g>`)
		i++
	}
	b.WriteString(`<g class="eyes-open"><path fill="` + mark.Hex[mark.Feature] +
		`" d="` + mark.CellsD(mark.Eyes["tall"], x0, y0, size) + `"/></g>`)
	b.WriteString(`<g class="eyes-shut"><path fill="` + mark.Hex[mark.Feature] +
		`" d="` + mark.CellsD(mark.Eyes["closed"], x0, y0, size) + `"/></g>`)
	return b.String()
}
