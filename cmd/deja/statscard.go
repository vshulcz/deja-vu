package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/stats"
)

const statsCardFont = "ui-monospace, SFMono-Regular, Menlo, monospace"

func writeStatsCard(path string, report stats.Report) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("stats card path: %w", err)
	}
	if err := os.WriteFile(abs, []byte(renderStatsCard(report)), 0o644); err != nil {
		// An unplugged disk arrived as `open /Volumes/…/card.png.svg: no such
		// file or directory` — an internal path and a syscall, the shape
		// #893/#907 replaced on the other writing paths (#1036).
		return "", fmt.Errorf("cannot write the stats card to %s — %s", abs, writeFailureReason(err))
	}
	return abs, nil
}

func renderStatsCard(r stats.Report) string {
	const (
		w, h = 800, 486
		pad  = 40
	)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", w, h, w, h)
	b.WriteString(`<defs>` + "\n")
	b.WriteString(`<pattern id="scan" width="4" height="3" patternUnits="userSpaceOnUse"><rect width="4" height="1" y="2" fill="#000000" fill-opacity="0.16"/></pattern>` + "\n")
	b.WriteString(`</defs>` + "\n")
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#0b0f10"/>`+"\n", w, h)
	fmt.Fprintf(&b, `<rect x="0.5" y="0.5" width="%d" height="%d" fill="none" stroke="#1e262a"/>`+"\n", w-1, h-1)
	b.WriteString(`<g font-family="` + statsCardFont + `" fill="#f4f7f7">` + "\n")

	// The mark, at a size that reads as the brand rather than as a favicon
	// someone left in the corner. Pixel rects: the same 24x22 grid everywhere.
	b.WriteString(`<g transform="translate(40,26) scale(2.4)">` + markStill(0, 0, 1) + `</g>` + "\n")
	cardText(&b, 116, 52, 17, "700", "deja-vu", "#8787af", "letter-spacing=\"0.5\"")
	cardText(&b, 205, 52, 14, "400", "· agent history", "#55626a")
	if span := valueOrDash(r.DateRange.Start) + " – " + valueOrDash(r.DateRange.End); span != "- – -" {
		cardText(&b, w-pad, 52, 13, "400", span, "#55626a", "text-anchor=\"end\"")
	}

	// The hero: one figure in the accent, its sentence beneath. The card led
	// with the whole sentence in white before, which is a headline with nothing
	// in it to look at first.
	value, caption := heroStat(r)
	cardText(&b, pad, 152, 62, "800", value, "#ff8700")
	cardText(&b, pad, 180, 15, "400", caption, "#8b989a")

	activity := "ACTIVITY"
	if n := streakDays(r.Heatmap); n > 1 {
		activity += "   ·   " + formatStatNumber(n) + " DAY STREAK"
	}
	cardText(&b, pad, 296, 11, "700", activity, "#55626a", "letter-spacing=\"1.5\"")
	renderHeatmap(&b, r.Heatmap, pad+4, 322)

	// The counts, supporting the hero rather than competing with it.
	for i, c := range cardCells(r) {
		x := pad + i*152
		cardText(&b, x, 234, 28, "700", c.value, "#f4f7f7")
		cardText(&b, x, 253, 12, "400", c.label, "#55626a")
	}

	renderAgents(&b, r, 470, 120)

	// Counts only, no content: see longestLine in statscard_term.go and #1180.
	if r.Longest.Messages > 0 {
		cardText(&b, pad, 472, 11, "700", "THE LONGEST SESSION", "#55626a", "letter-spacing=\"1.5\"")
		cardText(&b, pad+170, 472, 12, "400",
			formatStatNumber(r.Longest.Messages)+" messages", "#8b989a")
	}
	if n := r.RepeatQuestions; n > 0 {
		cardText(&b, w-pad, 472, 12, "400",
			formatStatNumber(n)+" questions asked more than once", "#8b989a",
			"text-anchor=\"end\"")
	}

	foot := "$ deja stats --card"
	if version != "" && version != "dev" {
		foot += " · v" + version
	}
	cardText(&b, pad, h-18, 11, "400", foot, "#55626a")
	cardText(&b, w-pad, h-18, 12, "700", "vshulcz.github.io/deja-vu", "#8787af", "text-anchor=\"end\"")
	b.WriteString("</g>\n")
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="url(#scan)"/>`+"\n", w, h)
	b.WriteString("</svg>\n")
	return b.String()
}

// renderAgents draws where the history came from. The bar sits in a track, so a
// short one reads as a small share rather than as a short row.
func renderAgents(b *strings.Builder, r stats.Report, x, y int) {
	if len(r.Harnesses) == 0 {
		return
	}
	harnesses := append([]stats.HarnessStats(nil), r.Harnesses...)
	sort.SliceStable(harnesses, func(i, j int) bool {
		if harnesses[i].Sessions == harnesses[j].Sessions {
			return harnesses[i].Harness < harnesses[j].Harness
		}
		return harnesses[i].Sessions > harnesses[j].Sessions
	})
	rest := 0
	if len(harnesses) > 3 {
		for _, hh := range harnesses[3:] {
			rest += hh.Sessions
		}
		harnesses = harnesses[:3:3]
	}
	max := 1
	for _, hh := range harnesses {
		if hh.Sessions > max {
			max = hh.Sessions
		}
	}

	cardText(b, x, y, 11, "700", "WHERE IT CAME FROM", "#55626a", "letter-spacing=\"1.5\"")
	// The bar runs from a fixed left edge to a fixed right one, and the count
	// sits beyond it. The first version anchored the count at the end of the
	// bar's own width, so a full bar and its number occupied the same pixels.
	const (
		barX  = 110
		track = 110
	)
	for i, hh := range harnesses {
		row := y + 22 + i*22
		cardText(b, x, row, 12, "400", hh.Harness, "#8b989a")
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="8" rx="4" fill="#161c1f"/>`+"\n",
			x+barX, row-8, track)
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="8" rx="4" fill="#8787af"/>`+"\n",
			x+barX, row-8, track*hh.Sessions/max)
		cardText(b, 760, row, 12, "700", formatStatNumber(hh.Sessions), "#f4f7f7",
			"text-anchor=\"end\"")
	}
	if rest > 0 {
		cardText(b, x, y+22+len(harnesses)*22, 11, "400",
			"and "+formatStatNumber(rest)+" more across the rest", "#55626a")
	}
}

// cardPunchline picks one personal, shareable sentence for the card hero.
func cardPunchline(r stats.Report) string {
	switch {
	case r.WeekRecalls > 0:
		return fmt.Sprintf("deja handed your agents memory %s time%s this week.", formatStatNumber(r.WeekRecalls), pluralS(r.WeekRecalls))
	case r.RepeatQuestions > 0:
		return fmt.Sprintf("You asked the same thing %s time%s — deja remembered.", formatStatNumber(r.RepeatQuestions), pluralS(r.RepeatQuestions))
	case r.Recall.Recalls+r.Recall.Injections > 0:
		handed := r.Recall.Recalls + r.Recall.Injections
		return fmt.Sprintf("deja handed your agents memory %s time%s.", formatStatNumber(handed), pluralS(handed))
	case r.TotalSessions > 0:
		return fmt.Sprintf("%s session%s of agent history, all searchable.", formatStatNumber(r.TotalSessions), pluralS(r.TotalSessions))
	default:
		return "Your coding-agent memory, indexed and searchable."
	}
}

// renderHeatmap draws a GitHub-style week-by-day grid with month ticks.
func renderHeatmap(b *strings.Builder, hm stats.HeatmapStats, x0, y0 int) {
	const step = 13
	for _, mt := range hm.Months {
		cardText(b, x0+mt.Col*step, y0-6, 10, "400", mt.Label, "#55626a")
	}
	for col, week := range hm.Weeks {
		for d := 0; d < 7; d++ {
			c := week[d]
			if c < 0 {
				continue
			}
			fill, op := heatFill(c, hm.Max)
			fmt.Fprintf(b, `<rect x="%d" y="%d" width="11" height="11" rx="2" fill="%s" opacity="%.2f"/>`+"\n",
				x0+col*step, y0+d*step, fill, op)
		}
	}
}

func heatFill(c, max int) (string, float64) {
	if c <= 0 {
		return "#141a1d", 1
	}
	ratio := 1.0
	if max > 0 {
		ratio = float64(c) / float64(max)
	}
	switch {
	case ratio <= 0.25:
		return "#8787af", 0.28
	case ratio <= 0.5:
		return "#8787af", 0.5
	case ratio <= 0.75:
		return "#8787af", 0.72
	default:
		return "#8787af", 1
	}
}

func cardText(b *strings.Builder, x, y, size int, weight, text, fill string, attrs ...string) {
	attr := ""
	if len(attrs) > 0 {
		attr = " " + strings.Join(attrs, " ")
	}
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="%d" font-weight="%s" fill="%s"%s>`, x, y, size, weight, fill, attr)
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(text))
	b.WriteString(escaped.String())
	b.WriteString("</text>\n")
}
