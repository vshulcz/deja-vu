package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
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
	return fmt.Sprintf("tip: %d sessions mention ssh — if you work across machines, `deja sync ssh <host>` carries this memory along (shown once)", sshSessions)
}
