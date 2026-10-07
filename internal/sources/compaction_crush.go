package sources

import (
	"fmt"
	"strings"
)

// Crush has no compaction event and fires only PreToolUse. Summarising writes
// a summary message into the same session and points the session row's
// summary_message_id at it; the turns before it stay in crush.db, and the
// agent only stops sending them (agent.go ListFromSummary, v0.97). So the next
// PreToolUse finds the summary there and reads the session as it stood before
// it, the way Gemini's next BeforeAgent does.

// ReadCrushCompaction captures the session as it stood before its newest
// summary. found is false when the session has never been summarised.
func ReadCrushCompaction(cwd, sessionID string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return CompactionTranscript{}, false, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	db := crushDBFor(cwd)
	if db == "" {
		return CompactionTranscript{}, false, nil
	}
	info, err := regularCompactionFile(db)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	id := sqlEscape(sessionID)
	rows, err := crushRows(db, "select json_object('id',cast(coalesce(summary_message_id,'') as text)) from sessions where id='"+id+"'")
	if err != nil || len(rows) != 1 || rows[0].ID == "" {
		return CompactionTranscript{}, false, err
	}
	summary := rows[0].ID
	where := " where s.id='" + id + "' and m.created_at < " +
		"(select created_at from messages where id='" + sqlEscape(summary) + "')"
	ss, err := parseCrushWhere(db, where)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if len(ss) != 1 || ss[0].ID != sessionID {
		return CompactionTranscript{}, false, nil
	}
	s := ss[0]
	return CompactionTranscript{
		Session: s, Harness: "crush", NativeSessionID: sessionID,
		// The store sits beside the work: Crush records no cwd per session.
		Workspace: CrushProjectDir(db), Path: db,
		// The summary, not the store: later turns change the store, and the
		// same compaction found again must keep its revision.
		Fingerprint: hashCompactionBytes([]byte("crush\x00" + sessionID + "\x00" + summary)),
		SourceSize:  info.Size(), SourceMTime: info.ModTime(),
	}, true, nil
}

// CrushStoreFor is the crush.db of the project cwd is in, "" when none.
func CrushStoreFor(cwd string) string { return crushDBFor(cwd) }
