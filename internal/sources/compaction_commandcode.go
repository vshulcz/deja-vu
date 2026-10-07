package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Command Code's shell hooks have no compaction event; its mods do, but a
// compaction is also written into the transcript, which every PreToolUse
// names. Compacting appends {"type":"compaction","summary","firstKeptEntryId"}
// and keeps every turn before it (1.77.0, stand), so the next tool call reads
// the session as it stood before the newest summary, the way Gemini's next
// BeforeAgent does.

// ReadCommandCodeCompaction captures the session in a Command Code transcript
// as it stood before its newest compaction. found is false when it has none.
func ReadCommandCodeCompaction(path, nativeSessionID string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, false, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	info, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	// Cheap first: most transcripts never compact.
	if !bytes.Contains(data, []byte(`"compaction"`)) {
		return CompactionTranscript{}, false, nil
	}
	var records []geminiRecord
	records = splitGeminiRecords(records, data, 0)
	if len(records) == 0 || !commandCodeHeaderFor(records[0].line, nativeSessionID) {
		return CompactionTranscript{}, false, fmt.Errorf("%w: not the command code session %q", ErrTranscriptIdentity, nativeSessionID)
	}
	cut := -1
	for i := len(records) - 1; i > 0; i-- {
		var rec struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(records[i].line, &rec) == nil && rec.Type == "compaction" {
			cut = i
			break
		}
	}
	if cut < 0 {
		return CompactionTranscript{}, false, nil
	}
	var prefix []byte
	for _, r := range records[:cut] {
		prefix = append(append(prefix, r.line...), '\n')
	}
	ss, err := parseCommandCodeWith(path, compactionMapScanner(path, prefix))
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if len(ss) != 1 || ss[0].ID != nativeSessionID {
		return CompactionTranscript{}, false, fmt.Errorf("%w: no parseable command code session", ErrUnsupportedCompactionTranscript)
	}
	boundary := records[cut]
	return CompactionTranscript{
		Session: ss[0], Harness: "commandcode", NativeSessionID: nativeSessionID,
		Workspace: CommandCodeSessionDir(path), Path: path,
		// The compaction, not the file: later turns append to the file, and
		// the same compaction found again must keep its revision.
		Fingerprint:       hashCompactionBytes(append(fmt.Appendf(nil, "%d\x00", boundary.off), boundary.line...)),
		HeaderFingerprint: hashCompactionBytes(records[0].line),
		SourceSize:        boundary.off, SourceMTime: info.ModTime(),
	}, true, nil
}

// commandCodeHeaderFor reports whether line is the v3 header of session id.
func commandCodeHeaderFor(line []byte, id string) bool {
	var h struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	return json.Unmarshal(line, &h) == nil && h.Type == "session" && h.ID == id
}
