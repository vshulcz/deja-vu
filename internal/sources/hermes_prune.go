package sources

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HermesPruneDue counts the sessions in a Hermes store that its own
// auto-prune deletes once they are older than retention, and that cross that
// line before now+window: ended sessions only, aged from their last activity —
// the latest of start, end and newest message — as Hermes ages them
// (website/docs/user-guide/sessions.md). ok is false when the store cannot be
// asked, so the caller says nothing rather than a wrong zero.
func HermesPruneDue(db string, retention, window time.Duration, now time.Time) (n int, ok bool) {
	if IsHermesPGStore(db) || !nonEmptyFile(db) {
		return 0, false
	}
	cols := map[string]bool{}
	out, err := sqliteOutput(db, `select name from pragma_table_info('sessions')`)
	if err != nil {
		return 0, false
	}
	for _, c := range strings.Fields(string(out)) {
		cols[c] = true
	}
	if !cols["ended_at"] || !cols["started_at"] {
		return 0, false
	}
	// Deleted at the first prune after last+retention, so due within the
	// window is last < now+window-retention.
	cutoff := float64(now.Add(window).Add(-retention).Unix())
	q := fmt.Sprintf(`select count(*) from sessions s where s.ended_at is not null and `+
		`max(s.started_at, s.ended_at, coalesce((select max(m.timestamp) from messages m where m.session_id = s.id), 0)) < %f`, cutoff)
	out, err = sqliteOutput(db, q)
	if err != nil {
		return 0, false
	}
	n, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return n, true
}
