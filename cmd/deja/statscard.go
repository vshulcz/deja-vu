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
	b.WriteString(`<rect width="800" height="420" rx="24" fill="#0d1117"/>` + "\n")
	b.WriteString(`<g font-family="` + statsCardFont + `" fill="#f0f6fc">` + "\n")
	cardText(&b, 40, 48, 25, "700", "deja", "#f78166")
	cardText(&b, 112, 47, 13, "400", "agent history", "#8b949e")
	cardText(&b, 760, 47, 13, "400", valueOrDash(r.DateRange.Start)+" - "+valueOrDash(r.DateRange.End), "#8b949e", "text-anchor=\"end\"")
	cardText(&b, 40, 91, 11, "400", "INDEXED WORK", "#8b949e", "letter-spacing=\"2\"")
	cardText(&b, 40, 132, 32, "700", fmt.Sprintf("%d", r.TotalSessions), "#f0f6fc")
	cardText(&b, 40, 153, 12, "400", "sessions", "#8b949e")
	cardText(&b, 190, 132, 32, "700", fmt.Sprintf("%d", r.TotalMessages), "#f0f6fc")
	cardText(&b, 190, 153, 12, "400", "messages", "#8b949e")
	cardText(&b, 340, 132, 32, "700", fmt.Sprintf("%d", len(r.Harnesses)), "#f0f6fc")
	cardText(&b, 340, 153, 12, "400", "harnesses", "#8b949e")

	cardText(&b, 40, 190, 11, "700", "ACTIVITY / LAST 12 MONTHS", "#8b949e", "letter-spacing=\"1.5\"")
	maxMonth := 0
	for _, m := range r.Monthly {
		if m.Messages > maxMonth {
			maxMonth = m.Messages
		}
	}
	for i, m := range r.Monthly {
		x := 40 + i*43
		h := 8
		opacity := 0.35
		if maxMonth > 0 && m.Messages > 0 {
			h = 8 + 52*m.Messages/maxMonth
			opacity = 0.35 + 0.65*float64(m.Messages)/float64(maxMonth)
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="27" height="%d" rx="4" fill="#58a6ff" opacity="%.2f"/>`+"\n", x, 255-h, h, opacity)
		label := m.Month
		if len(label) >= 7 {
			label = label[5:]
		}
		cardText(&b, x+13, 276, 10, "400", label, "#8b949e", "text-anchor=\"middle\"")
	}

	cardText(&b, 40, 312, 11, "700", "BY HARNESS", "#8b949e", "letter-spacing=\"1.5\"")
	harnesses := append([]harnessStats(nil), r.Harnesses...)
	sort.SliceStable(harnesses, func(i, j int) bool {
		if harnesses[i].Sessions == harnesses[j].Sessions {
			return harnesses[i].Harness < harnesses[j].Harness
		}
		return harnesses[i].Sessions > harnesses[j].Sessions
	})
	if len(harnesses) > 6 {
		other := harnessStats{Harness: "other"}
		for _, h := range harnesses[6:] {
			other.Sessions += h.Sessions
		}
		harnesses = append(harnesses[:6], other)
	}
	maxHarness := 1
	for _, h := range harnesses {
		if h.Sessions > maxHarness {
			maxHarness = h.Sessions
		}
	}
	for i, h := range harnesses {
		y := 322 + i*11
		cardText(&b, 40, y+9, 10, "400", h.Harness, "#c9d1d9")
		width := 100 * h.Sessions / maxHarness
		fmt.Fprintf(&b, `<rect x="145" y="%d" width="%d" height="8" rx="4" fill="#3fb950"/>`+"\n", y+1, width)
		cardText(&b, 255, y+9, 10, "700", fmt.Sprintf("%d", h.Sessions), "#c9d1d9")
	}
	if len(r.TopProjects) > 0 {
		cardText(&b, 500, 312, 11, "700", "TOP PROJECT", "#8b949e", "letter-spacing=\"1.5\"")
		cardText(&b, 500, 335, 13, "400", r.TopProjects[0].Project, "#c9d1d9")
	}
	cardText(&b, 40, 410, 11, "400", "deja v"+version, "#8b949e")
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
