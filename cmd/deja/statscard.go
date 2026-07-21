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
		return "", fmt.Errorf("write stats card: %w", err)
	}
	return abs, nil
}

func renderStatsCard(r stats.Report) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="420" viewBox="0 0 800 420">` + "\n")
	b.WriteString(`<defs>` + "\n")
	b.WriteString(`<linearGradient id="dg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#7c6cf0"/><stop offset="1" stop-color="#4ecdc4"/></linearGradient>` + "\n")
	b.WriteString(`<pattern id="scan" width="4" height="3" patternUnits="userSpaceOnUse"><rect width="4" height="1" y="2" fill="#000000" fill-opacity="0.16"/></pattern>` + "\n")
	b.WriteString(`</defs>` + "\n")
	b.WriteString(`<rect width="800" height="420" fill="#050807"/>` + "\n")
	b.WriteString(`<rect x="0.5" y="0.5" width="799" height="419" fill="none" stroke="#12291c"/>` + "\n")
	b.WriteString(`<g font-family="` + statsCardFont + `" fill="#d7f5e2">` + "\n")
	// the rewind-loop mark from logo.svg, then the wordmark — the same header
	// every deja surface carries
	b.WriteString(`<g transform="translate(36,26) scale(0.185)"><path d="M64 16 A48 48 0 1 0 112 64" fill="none" stroke="url(#dg)" stroke-width="14" stroke-linecap="round"/><path d="M112 64 l-20 -14 M112 64 l-24 6" fill="none" stroke="url(#dg)" stroke-width="14" stroke-linecap="round"/><circle cx="64" cy="64" r="8" fill="url(#dg)"/></g>` + "\n")
	cardText(&b, 70, 48, 15, "700", "deja-vu", "#4af08b", "letter-spacing=\"0.5\"")
	cardText(&b, 145, 48, 13, "400", "· agent history", "#5d8a6e")
	cardText(&b, 760, 48, 13, "400", valueOrDash(r.DateRange.Start)+" – "+valueOrDash(r.DateRange.End), "#5d8a6e", "text-anchor=\"end\"")
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
	cardText(&b, 44, 300, 30, "800", formatStatNumber(r.TotalSessions), "#4af08b")
	cardText(&b, 44, 320, 12, "400", "sessions", "#5d8a6e")
	cardText(&b, 196, 300, 30, "700", formatStatNumber(r.TotalMessages), "#d7f5e2")
	cardText(&b, 196, 320, 12, "400", "messages", "#5d8a6e")
	cardText(&b, 348, 300, 30, "700", fmt.Sprintf("%d", len(r.Harnesses)), "#d7f5e2")
	cardText(&b, 348, 320, 12, "400", "agents", "#5d8a6e")

	// top agents, right column
	cardText(&b, 470, 276, 11, "700", "TOP AGENTS", "#5d8a6e", "letter-spacing=\"1.5\"")
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
		cardText(&b, 470, y+9, 10, "400", h.Harness, "#a9cbb6")
		width := 90 * h.Sessions / maxHarness
		fmt.Fprintf(&b, `<rect x="570" y="%d" width="%d" height="8" rx="4" fill="#4af08b"/>`+"\n", y+1, width)
		cardText(&b, 672, y+9, 10, "700", fmt.Sprintf("%d", h.Sessions), "#a9cbb6")
	}

	cardText(&b, 40, 402, 11, "400", "$ deja stats --card · v"+version, "#5d8a6e")
	cardText(&b, 760, 402, 12, "700", "vshulcz.github.io/deja-vu", "#4af08b", "text-anchor=\"end\"")
	b.WriteString("</g>\n")
	b.WriteString(`<rect width="800" height="420" fill="url(#scan)"/>` + "\n")
	b.WriteString("</svg>\n")
	return b.String()
}

// cardPunchline picks one personal, shareable sentence for the card hero.
func cardPunchline(r stats.Report) string {
	switch {
	case r.WeekRecalls > 0:
		return fmt.Sprintf("deja handed your agents memory %s times this week.", formatStatNumber(r.WeekRecalls))
	case r.RepeatQuestions > 0:
		return fmt.Sprintf("You asked the same thing %s times — deja remembered.", formatStatNumber(r.RepeatQuestions))
	case r.Recall.Recalls > 0:
		return fmt.Sprintf("deja handed your agents memory %s times.", formatStatNumber(r.Recall.Recalls))
	case r.TotalSessions > 0:
		return fmt.Sprintf("%s sessions of agent history, all searchable.", formatStatNumber(r.TotalSessions))
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
		fmt.Fprintf(b, `<text x="%d" y="%d" font-size="%d" font-weight="800" fill="#eafff2">%s<tspan fill="#ffb454">%s</tspan></text>`+"\n",
			x, y, size, main.String(), tail.String())
		return
	}
	cardText(b, x, y, size, "800", head, "#eafff2")
}

// renderHeatmap draws a GitHub-style week-by-day grid with month ticks.
func renderHeatmap(b *strings.Builder, hm stats.HeatmapStats, x0, y0 int) {
	const step = 13
	for _, mt := range hm.Months {
		cardText(b, x0+mt.Col*step, y0-6, 10, "400", mt.Label, "#5d8a6e")
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
		return "#0b1410", 1
	}
	ratio := 1.0
	if max > 0 {
		ratio = float64(c) / float64(max)
	}
	switch {
	case ratio <= 0.25:
		return "#4af08b", 0.28
	case ratio <= 0.5:
		return "#4af08b", 0.5
	case ratio <= 0.75:
		return "#4af08b", 0.72
	default:
		return "#4af08b", 1
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
