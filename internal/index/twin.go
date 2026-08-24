package index

import "sort"

// TwinSessions pairs a session with the same session as it exists on another
// machine. Sync keeps both copies when an id is on two machines — neither
// overwrites the other, which is right — and the imported row keeps the id it
// had there (#1049). Nothing connected the two, so a reader saw one session's
// two histories as two unrelated sessions, sometimes with contradictory
// conclusions (#1775).
//
// The pairing is by harness and id: the same id under another harness is
// another session, and an import whose origin is not on this machine has no
// counterpart to name.
func TwinSessions(metas []SessionMeta) map[string][]string {
	local := make(map[string]string, len(metas))
	for _, m := range metas {
		if m.OrigID == "" {
			local[m.Harness+":"+m.ID] = m.Harness + ":" + m.ID
		}
	}
	out := map[string][]string{}
	for _, m := range metas {
		if m.OrigID == "" {
			continue
		}
		origin := m.Harness + ":" + m.OrigID
		if _, ok := local[origin]; !ok {
			continue
		}
		// Several machines can hold one session, so the local copy names all
		// of them; each import names the one it came from.
		imported := m.Harness + ":" + m.ID
		out[imported] = []string{origin}
		out[origin] = append(out[origin], imported)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}
