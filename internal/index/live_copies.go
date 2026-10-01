package index

// CopiesOf names the indexed sessions that are earlier copies of the given
// ones: a session forked into a new id carries its source's transcript, so
// hiding only the new id put the caller's own opening turn back on the page
// under the old one. Claude Code forks a session when it is moved to the
// background, and `--resume --fork-session` does the same by hand (#4210).
//
// A fork keeps the timestamps of what it copied, so the two open at the same
// instant, on the same title, in the same project, under the same harness —
// measured on real Claude Code forks, which open on the same millisecond. That
// is not enough on its own: subagent batches and opencode siblings open
// together too, and a fork that went on to its own work is not a copy of the
// session it came from. So a copy is also one whose transcript stops inside the
// live one: the copy's last turn is the live session's turn at the same place.
func CopiesOf(dir string, ids map[string]bool) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	var live []SessionMeta
	for _, meta := range m.Sessions {
		if ids[meta.ID] && !meta.Started.IsZero() && meta.Title != "" {
			live = append(live, meta)
		}
	}
	var out map[string]bool
	for _, meta := range m.Sessions {
		if ids[meta.ID] || meta.Started.IsZero() {
			continue
		}
		for _, l := range live {
			if meta.Harness != l.Harness || meta.Project != l.Project || meta.Title != l.Title || !meta.Started.Equal(l.Started) {
				continue
			}
			if !stopsInside(dir, m, meta, l) {
				continue
			}
			if out == nil {
				out = map[string]bool{}
			}
			out[meta.ID] = true
			break
		}
	}
	return out
}

// stopsInside reports whether copy's transcript ends on a turn live has at the
// same place. Only read for the few sessions that already share an opening.
func stopsInside(dir string, m Manifest, copy, live SessionMeta) bool {
	c, ok, err := loadSessionMeta(dir, m, copy)
	if err != nil || !ok || len(c.Messages) == 0 {
		return false
	}
	l, ok, err := loadSessionMeta(dir, m, live)
	if err != nil || !ok || len(l.Messages) < len(c.Messages) {
		return false
	}
	last := len(c.Messages) - 1
	a, b := c.Messages[last], l.Messages[last]
	return a.Role == b.Role && a.Text == b.Text && a.Time.Equal(b.Time)
}
