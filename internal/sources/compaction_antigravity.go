package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Antigravity has no compaction event: its hooks are PreToolUse, PostToolUse,
// PreInvocation, PostInvocation and Stop. A compaction writes a CHECKPOINT
// step and keeps the steps before it in transcript.jsonl, so the next
// PreInvocation finds it there and the session reads as it stood before it,
// the way Gemini's next BeforeAgent does.

// ReadAntigravityCompaction captures the conversation as it stood before the
// newest CHECKPOINT. found is false when the transcript has none. workspace is
// the one the hook payload names: the transcript does not record it.
func ReadAntigravityCompaction(path, conversationID, workspace string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(conversationID) == "" || antigravitySessionID(path) != conversationID {
		return CompactionTranscript{}, false, fmt.Errorf("%w: not the antigravity conversation %q", ErrTranscriptIdentity, conversationID)
	}
	info, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	size, mtime := info.Size(), info.ModTime()
	header, tail, tailOffset, truncated, err := readCompactionParts(path, size)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	after, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if !os.SameFile(info, after) || after.Size() != size || !after.ModTime().Equal(mtime) {
		return CompactionTranscript{}, false, ErrTranscriptChanging
	}
	var records []geminiRecord
	records = splitGeminiRecords(records, header, 0)
	records = splitGeminiRecords(records, tail, tailOffset)
	cut := -1
	for i := len(records) - 1; i >= 0; i-- {
		var step struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(records[i].line, &step) == nil && step.Type == "CHECKPOINT" {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return CompactionTranscript{}, false, nil
	}
	var prefix []byte
	for _, r := range records[:cut] {
		prefix = append(append(prefix, r.line...), '\n')
	}
	ss, err := parseAntigravityWith(path, compactionMapScanner(path, prefix))
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if len(ss) != 1 || ss[0].ID != conversationID {
		return CompactionTranscript{}, false, fmt.Errorf("%w: no parseable antigravity conversation", ErrUnsupportedCompactionTranscript)
	}
	boundary := records[cut]
	return CompactionTranscript{
		Session: ss[0], Harness: "antigravity", NativeSessionID: conversationID,
		Workspace: workspace, Path: path,
		// The checkpoint, not the file: later steps append to the file, and
		// the same compaction found again must keep its revision.
		Fingerprint:       hashCompactionBytes(append(fmt.Appendf(nil, "%d\x00", boundary.off), boundary.line...)),
		HeaderFingerprint: hashCompactionBytes(records[0].line),
		SourceSize:        boundary.off, SourceMTime: mtime,
		TailOffset: tailOffset, Truncated: truncated,
	}, true, nil
}
