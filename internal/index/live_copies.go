package index

// CopiesOf names the indexed sessions that are earlier copies of the given
// ones: a session forked into a new id carries its source's transcript, so
// hiding only the new id put the caller's own opening turn back on the page
// under the old one. Claude Code forks a session when it is moved to the
// background, and `--resume --fork-session` does the same by hand (#4210).
//
// A fork keeps the timestamps of what it copied, so the two open at the same
// instant, in the same project, under the same harness — measured on real
// Claude Code forks, which open on the same millisecond. An earlier session
// that asked the same thing at another time is not a copy, and stays. Where a
// harness stamps whole seconds, two sessions started together could share one,
// so there the opening has to match as well, by title.
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
		if ids[meta.ID] && !meta.Started.IsZero() {
			live = append(live, meta)
		}
	}
	var out map[string]bool
	for _, meta := range m.Sessions {
		if ids[meta.ID] || meta.Started.IsZero() {
			continue
		}
		for _, l := range live {
			if meta.Harness != l.Harness || meta.Project != l.Project || !meta.Started.Equal(l.Started) {
				continue
			}
			if meta.Started.Nanosecond() == 0 && (meta.Title == "" || meta.Title != l.Title) {
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
