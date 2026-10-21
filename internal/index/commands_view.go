package index

import (
	"slices"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// commandSessions is ss as the record log holds it, which is what a pass mines
// the command tables from: one session per row, its copies' turns in the order
// the build wrote them, less a turn another copy already gave and a blank one.
// A copy in another directory than the row's file, two transcripts sharing an
// id (#699), is left out: the row names its project and the tables count the
// copy the row names. A build that read the sessions instead filed the other
// copy's runs under its own project, counted every run of goose's migrated
// pairs twice, and kept whichever copy came last as the session's facts, none
// of which a pass reading the log could give again.
//
// ss must already be redacted as the records are, and in the order they were
// written.
func commandSessions(ss []model.Session, m Manifest) []model.Session {
	out := make([]model.Session, 0, len(ss))
	at := make(map[string]int, len(ss))
	seen := msgSeen{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		meta, ok := m.Sessions[key]
		if !ok || foreignPath(s.Path, meta) {
			continue
		}
		i, again := at[key]
		if !again {
			i = len(out)
			at[key] = i
			row := s
			row.Messages = nil
			row.Project = meta.Project
			out = append(out, row)
		}
		// The session's own slice while nothing is left out of it, which is
		// nearly every session: copying every message of a store doubled what
		// the sidecars held.
		var msgs []model.Message
		if again {
			msgs = slices.Clone(out[i].Messages)
		}
		whole := !again
		for j, msg := range s.Messages {
			if !seen.dup(key, msg.Role, msg.Time, msg.Text) && strings.TrimSpace(msg.Text) != "" {
				if !whole {
					msgs = append(msgs, msg)
				}
				continue
			}
			if whole {
				whole = false
				msgs = append([]model.Message(nil), s.Messages[:j]...)
			}
		}
		if whole {
			msgs = s.Messages
		}
		out[i].Messages = msgs
	}
	return out
}
