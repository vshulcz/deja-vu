package search

import (
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// CopyOf reports whether c is a copy of src that names no source: c holds
// src's last turn and src holds none of c's after it. A Claude --fork-session
// copy keeps every copied record's time, so it starts on the same record as
// its source and its start tells nothing apart (#5316). Two sessions that both
// went on are siblings, and neither is a copy of the other.
func CopyOf(c, src model.Session) bool {
	return holdsLastTurn(c, src) && !holdsLastTurn(src, c)
}

// holdsLastTurn reports whether holder has other's last timed turn, the same
// role, time and text. A reply the harness wrote on its own is passed over.
func holdsLastTurn(holder, other model.Session) bool {
	key := func(m model.Message) string {
		return m.Role + "\x00" + strconv.FormatInt(m.Time.UnixNano(), 10) + "\x00" + m.Text
	}
	for i := len(other.Messages) - 1; i >= 0; i-- {
		m := other.Messages[i]
		t := strings.TrimSpace(m.Text)
		if m.Time.IsZero() || t == "" || m.Role == roleSummary || (m.Role == "assistant" && t == "No response requested.") {
			continue
		}
		want := key(m)
		for _, h := range holder.Messages {
			if key(h) == want {
				return true
			}
		}
		return false
	}
	return false
}
