package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const statsCardFont = "ui-monospace, SFMono-Regular, Menlo, monospace"

func writeStatsCard(path string, report statsReport) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("stats card path: %w", err)
	}
	if err := os.WriteFile(abs, []byte(renderStatsCard(report)), 0o644); err != nil {
		return "", fmt.Errorf("write stats card: %w", err)
	}
	return abs, nil
}

func renderStatsCard(r statsReport) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="420" viewBox="0 0 800 420">` + "\n")
	b.WriteString(`<defs>` +
		`<linearGradient id="djHero" x1="0" y1="0" x2="1" y2="1">` +
		`<stop offset="0" stop-color="#7c6cf0"/><stop offset="1" stop-color="#4ecdc4"/></linearGradient>` +
		`<radialGradient id="djGlow" cx="0.5" cy="0.5" r="0.5">` +
		`<stop offset="0" stop-color="#7c6cf0" stop-opacity="0.30"/>` +
		`<stop offset="1" stop-color="#7c6cf0" stop-opacity="0"/></radialGradient>` +
		`</defs>` + "\n")
	b.WriteString(`<rect width="800" height="420" rx="24" fill="#0d0b16"/>` + "\n")
	b.WriteString(`<circle cx="720" cy="30" r="220" fill="url(#djGlow)"/>` + "\n")
	b.WriteString(`<rect x="1" y="1" width="798" height="418" rx="23" fill="none" stroke="#7c6cf0" stroke-opacity="0.18"/>` + "\n")
	b.WriteString(`<g font-family="` + statsCardFont + `" fill="#e6e6f0">` + "\n")
	// brand line (kept verbatim so the card is unmistakably deja) + active range
	b.WriteString(`<circle cx="45" cy="48" r="7" fill="url(#djHero)"/>` + "\n")
	cardText(&b, 62, 53, 15, "700", "deja · agent history", "#4ecdc4", "letter-spacing=\"0.5\"")
	cardText(&b, 760, 53, 13, "400", valueOrDash(r.DateRange.Start)+" – "+valueOrDash(r.DateRange.End), "#78788c", "text-anchor=\"end\"")
	// the shareable sentence — cap to two segments and size to fit the card width
	head := statsHeadline(r)
	if segs := strings.Split(head, " · "); len(segs) > 2 {
		head = strings.Join(segs[:2], " · ")
	}
	headSize := 21
	if n := len(head); n > 0 && 1200/n < headSize {
		if headSize = 1200 / n; headSize < 14 {
			headSize = 14
		}
	}
	cardText(&b, 40, 92, headSize, "700", head, "#e6e6f0")

	cardText(&b, 40, 124, 11, "700", "INDEXED WORK", "#78788c", "letter-spacing=\"2\"")
	cardText(&b, 40, 160, 34, "800", formatStatNumber(r.TotalSessions), "url(#djHero)")
	cardText(&b, 40, 180, 12, "400", "sessions", "#78788c")
	cardText(&b, 210, 160, 30, "700", formatStatNumber(r.TotalMessages), "#e6e6f0")
	cardText(&b, 210, 180, 12, "400", "messages", "#78788c")
	cardText(&b, 380, 160, 30, "700", fmt.Sprintf("%d", len(r.Harnesses)), "#e6e6f0")
	cardText(&b, 380, 180, 12, "400", "harnesses", "#78788c")

	cardText(&b, 40, 214, 11, "700", "ACTIVITY / LAST 12 MONTHS", "#78788c", "letter-spacing=\"1.5\"")
	maxMonth := 0
	for _, m := range r.Monthly {
		if m.Messages > maxMonth {
			maxMonth = m.Messages
		}
	}
	for i, m := range r.Monthly {
		x := 40 + i*43
		h := 8
		opacity := 0.30
		if maxMonth > 0 && m.Messages > 0 {
			h = 8 + 52*m.Messages/maxMonth
			opacity = 0.35 + 0.65*float64(m.Messages)/float64(maxMonth)
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="27" height="%d" rx="4" fill="url(#djHero)" opacity="%.2f"/>`+"\n", x, 278-h, h, opacity)
		label := m.Month
		if len(label) >= 7 {
			label = label[5:]
		}
		cardText(&b, x+13, 299, 10, "400", label, "#78788c", "text-anchor=\"middle\"")
	}

	cardText(&b, 40, 324, 11, "700", "BY HARNESS", "#78788c", "letter-spacing=\"1.5\"")
	harnesses := append([]harnessStats(nil), r.Harnesses...)
	sort.SliceStable(harnesses, func(i, j int) bool {
		if harnesses[i].Sessions == harnesses[j].Sessions {
			return harnesses[i].Harness < harnesses[j].Harness
		}
		return harnesses[i].Sessions > harnesses[j].Sessions
	})
	if len(harnesses) > 4 {
		other := harnessStats{Harness: "other"}
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
		y := 334 + i*11
		cardText(&b, 40, y+9, 10, "400", h.Harness, "#c9d1d9")
		width := 100 * h.Sessions / maxHarness
		fmt.Fprintf(&b, `<rect x="145" y="%d" width="%d" height="8" rx="4" fill="#4ecdc4"/>`+"\n", y+1, width)
		cardText(&b, 255, y+9, 10, "700", fmt.Sprintf("%d", h.Sessions), "#c9d1d9")
	}
	// No project names on the card: it is meant to be committed to public
	// READMEs, and project names are private. Show the active range instead.
	if r.DateRange.Start != "" {
		cardText(&b, 500, 324, 11, "700", "ACTIVE SINCE", "#78788c", "letter-spacing=\"1.5\"")
		cardText(&b, 500, 347, 13, "400", r.DateRange.Start, "#c9d1d9")
	}
	cardText(&b, 40, 405, 11, "400", "deja v"+version, "#78788c")
	cardText(&b, 760, 405, 12, "700", "vshulcz.github.io/deja-vu", "#4ecdc4", "text-anchor=\"end\"")
	b.WriteString("</g>\n</svg>\n")
	return b.String()
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
