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
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="420" viewBox="0 0 800 420">` + "\n")
	b.WriteString(`<defs>` + "\n")
	b.WriteString(`<pattern id="scan" width="4" height="3" patternUnits="userSpaceOnUse"><rect width="4" height="1" y="2" fill="#000000" fill-opacity="0.16"/></pattern>` + "\n")
	b.WriteString(`</defs>` + "\n")
	b.WriteString(`<rect width="800" height="420" fill="#0b0f10"/>` + "\n")
	b.WriteString(`<rect x="0.5" y="0.5" width="799" height="419" fill="none" stroke="#1e262a"/>` + "\n")
	b.WriteString(`<g font-family="` + statsCardFont + `" fill="#f4f7f7">` + "\n")
	// the cat from assets/logo.svg, then the wordmark. Pixel rects rather than
	// a scaled drawing: the mark is the same 24x22 grid wherever it appears.
	b.WriteString(`<g transform="translate(34,20) scale(1.5)"><path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v1h-18ZM2 6h18v1h-18ZM2 7h3v1h-3ZM7 7h8v1h-8ZM17 7h3v1h-3ZM2 8h3v1h-3ZM7 8h8v1h-8ZM17 8h3v1h-3ZM2 9h3v1h-3ZM7 9h8v1h-8ZM17 9h3v1h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v1h-2ZM5 15h12v1h-12ZM19 15h2v1h-2ZM5 16h12v1h-12ZM19 16h2v1h-2ZM5 17h12v1h-12ZM19 17h2v1h-2ZM5 18h12v1h-12ZM19 18h2v1h-2ZM4 19h16v1h-16ZM4 20h16v1h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#1c1c1c" d="M5 7h2v1h-2ZM15 7h2v1h-2ZM5 8h2v1h-2ZM15 8h2v1h-2ZM5 9h2v1h-2ZM15 9h2v1h-2Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/></g>` + "\n")
	cardText(&b, 84, 48, 15, "700", "deja-vu", "#8787af", "letter-spacing=\"0.5\"")
	cardText(&b, 159, 48, 13, "400", "· agent history", "#55626a")
	cardText(&b, 760, 48, 13, "400", valueOrDash(r.DateRange.Start)+" – "+valueOrDash(r.DateRange.End), "#55626a", "text-anchor=\"end\"")
	// the punch line — one personal sentence, sized to fit the card width
	head := cardPunchline(r)
	headSize := 25
	if n := len(head); n > 0 && 1150/n < headSize {
		if headSize = 1150 / n; headSize < 14 {
			headSize = 14
		}
	}
	renderPunchline(&b, 40, 90, headSize, head)

	// hero: a GitHub-style trailing-year activity grid
	renderHeatmap(&b, r.Heatmap, 44, 128)

	// supporting counts (sessions/messages kept as their own text nodes)
	cardText(&b, 44, 300, 30, "800", formatStatNumber(r.TotalSessions), "#ffffff")
	cardText(&b, 44, 320, 12, "400", "sessions", "#55626a")
	cardText(&b, 196, 300, 30, "700", formatStatNumber(r.TotalMessages), "#f4f7f7")
	cardText(&b, 196, 320, 12, "400", "messages", "#55626a")
	cardText(&b, 348, 300, 30, "700", fmt.Sprintf("%d", len(r.Harnesses)), "#f4f7f7")
	cardText(&b, 348, 320, 12, "400", "agents", "#55626a")

	// top agents, right column
	cardText(&b, 470, 276, 11, "700", "TOP AGENTS", "#55626a", "letter-spacing=\"1.5\"")
	harnesses := append([]stats.HarnessStats(nil), r.Harnesses...)
	sort.SliceStable(harnesses, func(i, j int) bool {
		if harnesses[i].Sessions == harnesses[j].Sessions {
			return harnesses[i].Harness < harnesses[j].Harness
		}
		return harnesses[i].Sessions > harnesses[j].Sessions
	})
	if len(harnesses) > 4 {
		other := stats.HarnessStats{Harness: "other"}
		for _, h := range harnesses[4:] {
			other.Sessions += h.Sessions
		}
		harnesses = append(harnesses[:4], other)
	}
	maxHarness := 1
	for _, h := range harnesses {
		if h.Sessions > maxHarness {
			maxHarness = h.Sessions
		}
	}
	for i, h := range harnesses {
		y := 290 + i*13
		cardText(&b, 470, y+9, 10, "400", h.Harness, "#8b989a")
		width := 90 * h.Sessions / maxHarness
		fmt.Fprintf(&b, `<rect x="570" y="%d" width="%d" height="8" rx="4" fill="#8787af"/>`+"\n", y+1, width)
		cardText(&b, 672, y+9, 10, "700", fmt.Sprintf("%d", h.Sessions), "#8b989a")
	}

	cardText(&b, 40, 402, 11, "400", "$ deja stats --card · v"+version, "#55626a")
	cardText(&b, 760, 402, 12, "700", "vshulcz.github.io/deja-vu", "#8787af", "text-anchor=\"end\"")
	b.WriteString("</g>\n")
	b.WriteString(`<rect width="800" height="420" fill="url(#scan)"/>` + "\n")
	b.WriteString("</svg>\n")
	return b.String()
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

// renderPunchline splits the headline on the em-dash so the "deja" clause
// prints in the accent color, matching the site's two-tone tagline.
func renderPunchline(b *strings.Builder, x, y, size int, head string) {
	if i := strings.Index(head, " — "); i > 0 {
		var main, tail strings.Builder
		_ = xml.EscapeText(&main, []byte(head[:i]))
		_ = xml.EscapeText(&tail, []byte(head[i:]))
		fmt.Fprintf(b, `<text x="%d" y="%d" font-size="%d" font-weight="800" fill="#ffffff">%s<tspan fill="#ff8700">%s</tspan></text>`+"\n",
			x, y, size, main.String(), tail.String())
		return
	}
	cardText(b, x, y, size, "800", head, "#ffffff")
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
