package main

import (
	"strings"
	"time"
)

func considerTime(minT, maxT *time.Time, t time.Time) {
	if t.IsZero() {
		return
	}
	if minT.IsZero() || t.Before(*minT) {
		*minT = t
	}
	if maxT.IsZero() || t.After(*maxT) {
		*maxT = t
	}
}

func firstMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

func sparkline(months []monthStats) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	maxMessages := 0
	for _, m := range months {
		if m.Messages > maxMessages {
			maxMessages = m.Messages
		}
	}
	var b strings.Builder
	for _, m := range months {
		idx := 0
		if maxMessages > 0 && m.Messages > 0 {
			idx = ((m.Messages - 1) * (len(blocks) - 1) / maxMessages) + 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

func monthLabels(months []monthStats) string {
	labels := make([]string, 0, len(months))
	for _, m := range months {
		if t, err := time.Parse("2006-01", m.Month); err == nil {
			labels = append(labels, t.Format("Jan"))
		}
	}
	return strings.Join(labels, " ")
}

func scaledBar(n, maxN, width int) int {
	if n <= 0 || maxN <= 0 {
		return 0
	}
	scaled := n * width / maxN
	if scaled == 0 {
		return 1
	}
	return scaled
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
