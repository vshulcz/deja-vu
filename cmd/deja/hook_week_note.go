package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// weekNoteEvery is how often the session-start hook says what the week
// looked like. A machine where deja served forty recalls this week looks,
// from the agent's UI, exactly like one where it served none; brief and
// stats know the difference, and nobody runs them (#3065).
const weekNoteEvery = 7 * 24 * time.Hour

// weekNote is that one line, or "". It goes out on the JSON path only — the
// plain path is the model's context, where a receipt to the person is noise.
//
// The first session after install starts the clock rather than reporting:
// "deja this week: 1 recall" a minute after the install would be the hook
// counting itself.
func weekNote(dir string) string {
	return weekNoteAt(dir, time.Now())
}

func weekNoteAt(dir string, now time.Time) string {
	p := dir + ".weeknote"
	if b, err := os.ReadFile(p); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && now.Sub(time.Unix(ts, 0)) < weekNoteEvery {
			return ""
		}
	} else {
		_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
		return ""
	}
	// Served and injected both are memory the agent got; the brief's own
	// week line counts them the same way.
	served, _, injected, _ := usage.Week(dir)
	recalls := served + injected
	if recalls == 0 {
		// A quiet week is not news; the clock restarts so the next week that
		// has something to say is the one reported.
		_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
		return ""
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
	line := fmt.Sprintf("deja this week: %d recall%s", recalls, pluralS(recalls))
	if dv := usage.DejaVuWeek(dir); dv > 0 {
		line += fmt.Sprintf(", %d déjà vu", dv)
	}
	return line + " — deja stats --card"
}
