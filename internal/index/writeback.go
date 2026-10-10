package index

import (
	"os"
)

// AdoptWriteBack records a transcript `deja resume --write-back` rebuilt from
// the index (#4617) as the file the index already read. The rebuilt file
// holds the conversation only, while the index still holds the session's
// commands, edits and tool output; re-reading it would replace those with
// less. Recorded as read, the next pass sees it unchanged, and once the agent
// appends to it, reads only what was appended.
//
// kept is the path the index read the session from and path the one written,
// which differ when codex had compressed the rollout: the written file is
// plain JSONL beside it, and the index's rows move to it. ok is false when
// the index has no entry for kept, and nothing is changed then.
func AdoptWriteBack(dir, kept, path string) (ok bool, err error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return false, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return false, err
	}
	of, held := m.Files[kept]
	if !held {
		return false, nil
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	fs := walkFileState(path, fi, nil)
	fs.LastUpdated = of.LastUpdated
	fs.Redactions = of.Redactions
	m.Files[path] = fs
	if kept == path {
		return true, writeManifestOnly(dir, m)
	}
	delete(m.Files, kept)
	if e, ok := m.IngestFiles[kept]; ok {
		m.IngestFiles[path] = e
		delete(m.IngestFiles, kept)
	}
	for key, meta := range m.Sessions {
		if meta.Path == kept {
			meta.Path = path
			m.Sessions[key] = meta
		}
	}
	return true, writeManifestMeta(dir, m)
}
