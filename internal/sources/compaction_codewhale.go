package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CodeWhale has no compaction hook. Before it compacts it saves the whole
// history as <sessions>/<id>/artifacts/context-transfer-<checkpoint>.json, and
// once the model has written the summary it saves that beside it under the
// same name in .md (compact_messages_safe, crates/tui/src/compaction.rs; both
// names are in the 0.10.0 binary, and a 0.10.0 stand wrote them). A .json with
// no .md is an attempt that failed or only pruned tool output, which drops no
// turns.
//
// The hooks are told a session id of their own, sess_<8 hex> made fresh for
// each hook executor (hooks/executor.rs), not the saved session's, so the
// session is found by its workspace: the one there whose newest summary is
// newest, written after since.

// ReadCodeWhaleCompaction captures the CodeWhale session in workspace as it
// stood before the newest compaction written after since. found is false when
// there is none.
func ReadCodeWhaleCompaction(workspace string, since time.Time) (CompactionTranscript, bool, error) {
	want := sameDirKey(workspace)
	if want == "" {
		return CompactionTranscript{}, false, nil
	}
	var best, bestRoot, bestID string
	var bestAt time.Time
	for _, root := range CodeWhaleRoots() {
		dirs, _ := filepath.Glob(filepath.Join(root, "*", "artifacts", "context-transfer-*.md"))
		for _, md := range dirs {
			info, err := os.Stat(md)
			if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) || !info.ModTime().After(bestAt) {
				continue
			}
			if _, err := os.Stat(strings.TrimSuffix(md, ".md") + ".json"); err != nil {
				continue
			}
			id := filepath.Base(filepath.Dir(filepath.Dir(md)))
			if sameDirKey(CodeWhaleWorkspace(filepath.Join(root, id+".json"))) != want {
				continue
			}
			best, bestRoot, bestID, bestAt = md, root, id, info.ModTime()
		}
	}
	if best == "" {
		return CompactionTranscript{}, false, nil
	}
	history := strings.TrimSuffix(best, ".md") + ".json"
	info, err := regularCompactionFile(history)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	b, err := os.ReadFile(history)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	var doc codeWhaleSession
	if err := json.Unmarshal(b, &doc.Messages); err != nil {
		return CompactionTranscript{}, false, fmt.Errorf("%w: %v", ErrUnsupportedCompactionTranscript, err)
	}
	// The session file has what the history does not: the id, title,
	// workspace and start.
	session := filepath.Join(bestRoot, bestID+".json")
	if meta, err := os.ReadFile(session); err == nil {
		var head codeWhaleSession
		if json.Unmarshal(meta, &head) == nil {
			doc.Metadata = head.Metadata
		}
	}
	if doc.Metadata.ID == "" {
		doc.Metadata.ID = bestID
	}
	ss := parseCodeWhaleDoc(session, doc)
	if len(ss) != 1 {
		return CompactionTranscript{}, false, nil
	}
	checkpoint := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(best), "context-transfer-"), ".md")
	return CompactionTranscript{
		Session: ss[0], Harness: "codewhale", NativeSessionID: ss[0].ID,
		Workspace: doc.Metadata.Workspace, Path: history,
		Fingerprint: hashCompactionBytes([]byte("codewhale\x00" + ss[0].ID + "\x00" + checkpoint)),
		SourceSize:  info.Size(), SourceMTime: info.ModTime(),
	}, true, nil
}

// sameDirKey is a directory spelled so two spellings of it compare equal:
// absolute, cleaned and with symlinks resolved (/tmp and /private/tmp on
// macOS).
func sameDirKey(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs)
}
