package sources

import (
	"encoding/json"
	"fmt"
	"strings"
)

// goose has no compaction event deja can hook, and it drops what its hooks
// print. Compacting keeps the old messages in sessions.db with metadata
// agentVisible=false, then adds an agent-only summary and an agent-only note
// that starts "Your context was compacted" (goose 1.46). So the next Stop
// finds that note and reads the session as it stood before it.

const gooseCompactedNote = "Your context was compacted"

// ReadGooseCompaction captures the session as it stood before its newest
// compaction. found is false when it never compacted.
func ReadGooseCompaction(sessionID string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return CompactionTranscript{}, false, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	id := sqlEscape(sessionID)
	hidden := "coalesce(json_extract(m.metadata_json,'$.userVisible'),1) in (0,'false')"
	q := `select json_object('id',m.id,'at',m.created_timestamp,'n',(select count(*) from messages x where x.session_id=m.session_id and x.content_json like '%` + gooseCompactedNote + `%')) ` +
		`from messages m where m.session_id='` + id + `' and m.content_json like '%` + gooseCompactedNote + `%' and ` + hidden +
		` order by m.created_timestamp desc, m.id desc limit 1`
	for _, db := range GooseDBs() {
		if !nonEmptyFile(db) || GooseStoreLacks(db, sessionID) {
			continue
		}
		info, err := regularCompactionFile(db)
		if err != nil {
			return CompactionTranscript{}, false, err
		}
		out, err := sqliteOutput(db, q)
		if err != nil {
			return CompactionTranscript{}, false, err
		}
		var b struct {
			ID int64 `json:"id"`
			At int64 `json:"at"`
			N  int64 `json:"n"`
		}
		if strings.TrimSpace(string(out)) == "" || json.Unmarshal(out, &b) != nil || b.ID == 0 {
			return CompactionTranscript{}, false, nil
		}
		// What the person saw, up to the note: the summary is agent-only, and
		// so is every earlier compaction's.
		where := fmt.Sprintf(" and s.id='%s' and (m.created_timestamp < %d or (m.created_timestamp = %d and m.id < %d)) and not (%s)",
			id, b.At, b.At, b.ID, hidden)
		ss, err := parseGooseDBWhere(db, where, 0)
		if err != nil {
			return CompactionTranscript{}, false, err
		}
		if len(ss) != 1 || ss[0].ID != sessionID {
			return CompactionTranscript{}, false, nil
		}
		dir, _ := sqliteOutput(db, "select working_dir from sessions where id='"+id+"'")
		return CompactionTranscript{
			Session: ss[0], Harness: "goose", NativeSessionID: sessionID,
			Workspace: strings.TrimSpace(string(dir)), Path: db,
			// goose rewrites the whole conversation on compaction, so row ids
			// move; how many compactions and when the newest one was do not.
			Fingerprint: hashCompactionBytes(fmt.Appendf(nil, "goose\x00%s\x00%d\x00%d", sessionID, b.N, b.At)),
			SourceSize:  info.Size(), SourceMTime: info.ModTime(),
		}, true, nil
	}
	return CompactionTranscript{}, false, nil
}
