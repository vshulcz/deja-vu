package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// monthlyTotal is how much work falls inside the window the sparkline draws.
// Twelve empty bars is what a broken index looks like, and a store whose work
// is simply older than a year draws exactly that (#703).
func monthlyTotal(months []stats.MonthStats) int {
	n := 0
	for _, m := range months {
		n += m.Messages
	}
	return n
}

func monthLabels(months []stats.MonthStats) string {
	labels := make([]string, 0, len(months))
	for _, m := range months {
		if t, err := time.Parse("2006-01", m.Month); err == nil {
			labels = append(labels, t.Format("Jan"))
		}
	}
	return strings.Join(labels, " ")
}

// monthChart draws one bar per month, three cells wide and three rows tall,
// with the month's name right under its own bar. The old chart was a
// twelve-cell sparkline followed by twelve labels, so no label sat under the
// bar it named. Without colour (a pipe, NO_COLOR) a cell is "#" or blank, the
// same glyph the project bars use there.
func monthChart(months []stats.MonthStats, color bool) []string {
	const rows = 3
	peak := 0
	for _, m := range months {
		peak = max(peak, m.Messages)
	}
	eighths := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	lines := make([]string, rows+1)
	for i, m := range months {
		// Height in eighths of a cell; any month with work shows at least one.
		h := 0
		if peak > 0 && m.Messages > 0 {
			h = max(1, m.Messages*rows*8/peak)
		}
		for row := 0; row < rows; row++ {
			fill := min(max(h-(rows-1-row)*8, 0), 8)
			cell := eighths[fill]
			if !color {
				cell = " "
				if fill >= 4 || (row == rows-1 && h > 0) {
					cell = "#"
				}
			}
			if i > 0 {
				lines[row] += " "
			}
			lines[row] += strings.Repeat(cell, 3)
		}
		label := "   "
		if t, err := time.Parse("2006-01", m.Month); err == nil {
			label = t.Format("Jan")
		}
		if i > 0 {
			lines[rows] += " "
		}
		lines[rows] += label
	}
	for i := range lines[:rows] {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sshSyncTip suggests `deja sync ssh` once when the history shows the user
// working across machines — many sessions mentioning ssh is the signal that
// their memory is fragmented over hosts. One-time: a sentinel next to the
// index suppresses repeats, and any sync usage counts as "already knows".
func sshSyncTip(dir string, ss []model.Session) string {
	sentinel := dir + ".synctip"
	if _, err := os.Stat(sentinel); err == nil {
		return ""
	}
	sshSessions := 0
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "ssh ") || strings.Contains(m.Text, "ssh-") {
				sshSessions++
				break
			}
		}
	}
	if sshSessions < 5 {
		return ""
	}
	_ = os.WriteFile(sentinel, []byte("shown"), 0o600)
	return fmt.Sprintf("tip: %d session%s mention ssh — if you work across machines, `deja sync ssh <host>` carries this memory along (shown once)", sshSessions, pluralS(sshSessions))
}

// rawSize is the transcript volume a set of served sessions represents — the
// denominator of the served-vs-replayed ratio.
func rawSize(ss []model.Session) int64 {
	var n int64
	for _, s := range ss {
		for _, m := range s.Messages {
			n += int64(len(m.Text))
		}
	}
	return n
}
