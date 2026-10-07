package sources

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ZCode has no compaction hook (seven events, none of them PreCompact), and
// the transcript_path its hooks get is a one-line temp file it writes for the
// call (createClaudeCompatibleHookStdin, 3.14.4). The CLI database keeps the
// session whole: a compaction adds a user message whose semantics.kind is
// compact_summary, with a compaction part, and the turns it summarised stay
// (stand on 3.14.4). The next prompt or tool call reads the session as it
// stood before the newest summary.

// ZCodeHookTranscript reports whether a hook payload's transcript_path is the
// temp file ZCode writes for a hook call, which is how a payload says it came
// from ZCode.
func ZCodeHookTranscript(path string) bool {
	return path != "" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), "zcode-claude-hook-")
}

// ReadZCodeCompaction captures a ZCode session as it stood before its newest
// compaction summary. found is false when it has none.
func ReadZCodeCompaction(sessionID string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return CompactionTranscript{}, false, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	db := ZCodeDB()
	info, err := regularCompactionFile(db)
	if err != nil {
		return CompactionTranscript{}, false, nil
	}
	id := sqlEscape(sessionID)
	cmd, stop := sqliteReadCmd(db, "select json_object('id',id,'at',time_created) from message where session_id='"+id+
		"' and json_extract(data,'$.semantics.kind')='compact_summary' order by time_created desc, id desc limit 1")
	b, err := cmd.Output()
	stop()
	if err != nil || len(b) == 0 {
		return CompactionTranscript{}, false, err
	}
	rows, err := sqliteObjects[struct {
		ID string `json:"id"`
		At int64  `json:"at"`
	}](b)
	if err != nil || len(rows) != 1 || rows[0].ID == "" {
		return CompactionTranscript{}, false, err
	}
	summary := rows[0]
	ss, err := parseOpencodeSchemaDB("zcode", db, fmt.Sprintf(" and s.id='%s' and m.time_created < %d", id, summary.At), 0)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if len(ss) != 1 || ss[0].ID != sessionID {
		return CompactionTranscript{}, false, nil
	}
	s := ss[0]
	return CompactionTranscript{
		Session: s, Harness: "zcode", NativeSessionID: sessionID,
		// The reader keeps the session's directory as its path.
		Workspace: s.Path, Path: db,
		// The summary, not the store: later turns change the store, and the
		// same compaction found again must keep its revision.
		Fingerprint: hashCompactionBytes([]byte("zcode\x00" + sessionID + "\x00" + summary.ID)),
		SourceSize:  info.Size(), SourceMTime: info.ModTime(),
	}, true, nil
}
