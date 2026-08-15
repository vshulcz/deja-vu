package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/mark"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// The card, drawn where the user already is.
//
// It was an SVG first, which means the shareable summary of someone's own work
// arrives as a file they have to go and open. The terminal is the surface the
// rest of deja lives on, so that is where this belongs; the SVG stays for the
// places a terminal cannot go, like a profile README.

// The heatmap ramp: four steps in the coat's own hue, because the terminal has
// no opacity and fading a colour toward the background by hand is what makes a
// grid look muddy. Empty days take a grey just above the background — visible
// as structure, never as activity.
//
// The steps have to be far apart. The first version ran 60, 61, 103, 146 with
// empty at 236, and 60 against 236 is two darks: the grid rendered as one flat
// field and none of the four steps could be told from another.
var heatRamp = [4]int{60, 103, 146, 189}

const heatEmpty = 235

func weekTotal(week [7]int) int {
	total := 0
	for _, c := range week {
		if c > 0 {
			total += c
		}
	}
	return total
}

func termHeat(count, max int) int {
	if count <= 0 {
		return heatEmpty
	}
	ratio := 1.0
	if max > 0 {
		ratio = float64(count) / float64(max)
	}
	switch {
	case ratio <= 0.25:
		return heatRamp[0]
	case ratio <= 0.5:
		return heatRamp[1]
	case ratio <= 0.75:
		return heatRamp[2]
	default:
		return heatRamp[3]
	}
}

// dim and bright are the two text weights the card uses; the numbers get bright
// and everything naming them gets dim, so the eye lands on the figures.
const (
	cardDim    = 244
	cardBright = 231
)

func termFG(n int) string { return "\x1b[38;5;" + strconv.Itoa(n) + "m" }

func paint(colour int, s string) string { return termFG(colour) + s + logoReset }

// termCardWidth is the widest the card gets: a year of weeks is 53 columns, and
// the rest is laid out around that rather than the other way round.
const termCardWidth = 53

// renderStatsCard writes the card as lines of text. It returns lines rather
// than printing so the caller can frame or indent them, and so a test can read
// what it drew without a terminal.
func statsCardLines(r stats.Report) []string {
	var out []string
	art := renderCat(moodReady)

	head := []string{
		paint(cardBright, "deja-vu") + "  " + paint(cardDim, "agent history"),
		"",
		cardPunchline(r),
		"",
		paint(cardDim, dateSpan(r)),
	}
	// The figures sit in the cat's lower half rather than under it: eleven
	// lines of animal beside three lines of text left a blank band down the
	// right, which is the same hole the demo had.
	head = append(head, "", "")
	head = append(head, strings.Split(statRow(r), "\n")...)
	for i, line := range art {
		text := ""
		if i < len(head) {
			text = head[i]
		}
		out = append(out, strings.TrimRight(line+"  "+text, " "))
	}
	out = append(out, "")
	out = append(out, heatLines(r.Heatmap)...)
	if top := topAgents(r); len(top) > 0 {
		out = append(out, "")
		out = append(out, top...)
	}
	return out
}

func dateSpan(r stats.Report) string {
	start, end := valueOrDash(r.DateRange.Start), valueOrDash(r.DateRange.End)
	if start == "-" && end == "-" {
		return ""
	}
	return start + " – " + end
}

// statRow is the three figures, spaced across the heatmap's own width so the
// card reads as one column rather than two things that happen to be stacked.
func statRow(r stats.Report) string {
	cells := []struct{ value, label string }{
		{formatStatNumber(r.TotalSessions), "sessions"},
		{formatStatNumber(r.TotalMessages), "messages"},
		{strconv.Itoa(len(r.Harnesses)), "agents"},
	}
	var values, labels []string
	for _, c := range cells {
		w := len(c.value)
		if len(c.label) > w {
			w = len(c.label)
		}
		values = append(values, paint(cardBright, pad(c.value, w)))
		labels = append(labels, paint(cardDim, pad(c.label, w)))
	}
	return strings.Join(values, "   ") + "\n" + strings.Join(labels, "   ")
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// heatLines draws the year as four rows of half blocks: one cell carries two
// days, the upper one as the foreground and the lower one as the background.
//
// Seven rows of one cell per day was the obvious shape and the wrong one. It
// wanted a small-square glyph to leave gaps between cells, and the width of ▪
// varies by font — in something people screenshot, a character that is narrow
// in one terminal and full-width in another breaks the layout with nothing to
// warn you. A full block is the one shape every monospace font agrees on.
func heatLines(hm stats.HeatmapStats) []string {
	if len(hm.Weeks) == 0 {
		return nil
	}
	// Drawing the whole year when the index covers three months of it spends
	// two thirds of the card on a grey rectangle standing for history that
	// does not exist. Start at the first week with anything in it.
	weeks, first := hm.Weeks, 0
	for first < len(weeks) && weekTotal(weeks[first]) == 0 {
		first++
	}
	if first == len(weeks) {
		return nil
	}
	// A couple of empty weeks in front give the first activity an edge to sit
	// against rather than starting hard at the margin.
	if first > 2 {
		first -= 2
	} else {
		first = 0
	}
	weeks = weeks[first:]

	months := make([]byte, len(weeks)+8)
	for i := range months {
		months[i] = ' '
	}
	for _, mt := range hm.Months {
		col := mt.Col - first
		if col >= 0 && col+len(mt.Label) < len(months) {
			copy(months[col:], mt.Label)
		}
	}
	lines := []string{paint(cardDim, strings.TrimRight(string(months), " "))}
	// Seven days is odd, so the last row carries one day over an empty one.
	for d := 0; d < 7; d += 2 {
		var b strings.Builder
		for _, week := range weeks {
			top := termHeat(week[d], hm.Max)
			bottom := heatEmpty
			if d+1 < 7 {
				bottom = termHeat(week[d+1], hm.Max)
			}
			b.WriteString(fgColour(top) + bgColour(bottom) + "\u2580" + logoReset)
		}
		lines = append(lines, b.String())
	}
	return lines
}

// topAgents lists where the history came from, longest first, with a bar drawn
// to the widest entry rather than to the total: the shape of the split is the
// readable part, not each share of a whole.
func topAgents(r stats.Report) []string {
	if len(r.Harnesses) == 0 {
		return nil
	}
	ranked := append([]stats.HarnessStats(nil), r.Harnesses...)
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].Sessions > ranked[j-1].Sessions; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	if len(ranked) > 5 {
		other := stats.HarnessStats{Harness: "other"}
		for _, h := range ranked[5:] {
			other.Sessions += h.Sessions
		}
		ranked = append(ranked[:5:5], other)
	}
	name := 0
	for _, h := range ranked {
		if len(h.Harness) > name {
			name = len(h.Harness)
		}
	}
	max := ranked[0].Sessions
	lines := []string{paint(cardDim, "TOP AGENTS")}
	for _, h := range ranked {
		width := 0
		if max > 0 {
			width = h.Sessions * (termCardWidth - name - 8) / max
		}
		if width < 1 && h.Sessions > 0 {
			width = 1
		}
		lines = append(lines, fmt.Sprintf("%s %s %s",
			paint(cardDim, pad(h.Harness, name)),
			paint(mark.Coat, strings.Repeat("▬", width)),
			paint(cardBright, strconv.Itoa(h.Sessions))))
	}
	return lines
}

func printStatsCard(w io.Writer, r stats.Report) {
	for _, line := range statsCardLines(r) {
		fmt.Fprintln(w, line)
	}
}
