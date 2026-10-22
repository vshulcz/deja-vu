package index

import "maps"

// setAsideEmpty moves key's row from Sessions to Empty: a session with no
// record is not listed, as a rebuild lists none (#868, #5563). Its Ord goes,
// so a later session can take the number; one taken back gets a fresh one.
func setAsideEmpty(m *Manifest, key string) {
	row, ok := m.Sessions[key]
	if !ok {
		return
	}
	delete(m.Sessions, key)
	if m.Empty == nil {
		m.Empty = map[string]SessionMeta{}
	}
	row.Ord = 0
	m.Empty[key] = row
}

// keepEmpty is the Empty a pass carries over: its own copy, without the rows
// whose session now has a row of its own or whose file is gone from files.
func keepEmpty(old map[string]SessionMeta, sessions map[string]SessionMeta, files map[string]FileState) map[string]SessionMeta {
	if len(old) == 0 {
		return nil
	}
	out := maps.Clone(old)
	for key, row := range out {
		if _, listed := sessions[key]; listed {
			delete(out, key)
			continue
		}
		if _, ok := files[row.Path]; !ok {
			delete(out, key)
		}
	}
	return out
}
