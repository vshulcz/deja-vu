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
// written. filedUnder, when set, is the file the build filed a turn under
// where that is not its session's own: a turn a deleted copy shares, which
// stays the deleted copy's (see keptTurns). The table goes by that file, as a
// pass reading the record does.
func commandSessions(ss []model.Session, m Manifest, filedUnder func(key string, s model.Session, msg model.Message) string) []model.Session {
	out := make([]model.Session, 0, len(ss))
	at := make(map[string]int, len(ss))
	seen := msgSeen{}
	for _, s := range ss {
		key := s.Harness + ":" + s.ID
		meta, ok := m.Sessions[key]
		if !ok {
			continue
		}
		foreign := foreignPath(s.Path, meta)
		i, again := at[key]
		// The session's own slice while nothing is left out of it, which is
		// nearly every session: copying every message of a store doubled what
		// the sidecars held.
		var msgs []model.Message
		if again {
			msgs = slices.Clone(out[i].Messages)
		}
		whole := !again
		for j, msg := range s.Messages {
			left := seen.dup(key, msg.Role, msg.Time, msg.Text) || strings.TrimSpace(msg.Text) == ""
			if !left {
				left = foreign
				if filedUnder != nil {
					if p := filedUnder(key, s, msg); p != "" {
						left = foreignPath(p, meta)
					}
				}
			}
			if !left {
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
		if !again {
			if foreign && len(msgs) == 0 {
				continue
			}
			i = len(out)
			at[key] = i
			row := s
			row.Project = meta.Project
			out = append(out, row)
		}
		out[i].Messages = msgs
	}
	return out
}
