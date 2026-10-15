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

// weekNoteAt is that one line, or "". It goes out on the JSON path only — the
// plain path is the model's context, where a receipt to the person is noise.
//
// The first session after install starts the clock rather than reporting:
// "deja this week: 1 recall" a minute after the install would be the hook
// counting itself.
func weekNoteAt(dir string, now time.Time) string { return weekNoteFor(dir, now, true) }

// weekNoteFor is the week note for a reader that may not be the owner alone;
// see builtNoteFor. The second line names the session agents used most, and
// for a shared reader it names the agent and the id but never the title,
// which is the person's own question.
func weekNoteFor(dir string, now time.Time, private bool) string {
	p := dir + ".weeknote"
	since := now.Add(-weekNoteEvery)
	if b, err := os.ReadFile(p); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			if now.Sub(time.Unix(ts, 0)) < weekNoteEvery {
				return ""
			}
			since = time.Unix(ts, 0)
		}
	} else {
		_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
		return ""
	}
	// Served and injected both are memory the agent got; the brief's own
	// week line counts them the same way. One read for both this and the déjà
	// vu count below: the note is written from a session-start hook, and two
	// walks of the log can also report a recall count and a déjà vu count that
	// were never true together (#1576).
	n := usage.StatusCounters(dir)
	recalls := n.WeekRecalls + n.WeekInjections
	// Counted from the last note, so each deletion is said once.
	deleted := weekDeletedLine(dir, weekDeletedSince(dir, since, now), since, now)
	if recalls == 0 {
		// A quiet week is not news; the clock restarts so the next week that
		// has something to say is the one reported. Sessions deleted under
		// the person are, quiet week or not.
		_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
		return deleted
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
	line := fmt.Sprintf("deja this week: %d recall%s", recalls, pluralS(recalls))
	if n.DejaVuWeek > 0 {
		line += fmt.Sprintf(", %d déjà vu", n.DejaVuWeek)
	}
	line += " — deja stats --card"
	return joinNotes(joinNotes(line, weekUsedLine(dir, now, private)), deleted)
}

// humanNotes is what deja has to tell the person rather than the model: the
// one-time built note, the one-time what-changed note for a new version, the
// weekly count, and the once-per-index GitHub line. One of them per call, the
// first that is due: four at once made a session start a wall of text. Each
// marks itself shown only when it is returned, so the rest wait for the next
// session start rather than being spent unseen.
func humanNotes(dir string) string { return humanNotesFor(dir, true) }

// humanNotesFor is humanNotes for a reader that may not be the owner alone;
// see builtNoteFor.
func humanNotesFor(dir string, private bool) string {
	built := func(dir string) string { return builtNoteFor(dir, private) }
	week := func(dir string) string { return weekNoteFor(dir, time.Now(), private) }
	for _, note := range []func(string) string{built, whatChangedNote, week, pendingStar} {
		if line := note(dir); line != "" {
			return line
		}
	}
	return ""
}

// pendingStar is the GitHub line for a person who has not seen it: on the
// hook path that is a plugin or package install, which has no install proof
// to carry it. It waits until the built note has been shown, so it follows
// an index that found something, never an empty machine.
func pendingStar(dir string) string {
	if _, err := os.Stat(dir + ".builtnote"); err != nil {
		return ""
	}
	return starLine(dir)
}
