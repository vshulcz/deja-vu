package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func monthLabels(months []stats.MonthStats) string {
	labels := make([]string, 0, len(months))
	for _, m := range months {
		if t, err := time.Parse("2006-01", m.Month); err == nil {
			labels = append(labels, t.Format("Jan"))
		}
	}
	return strings.Join(labels, " ")
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
	return fmt.Sprintf("tip: %d sessions mention ssh — if you work across machines, `deja sync ssh <host>` carries this memory along (shown once)", sshSessions)
}
