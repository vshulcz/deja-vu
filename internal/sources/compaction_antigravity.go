package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Antigravity has no compaction event: its hooks are PreToolUse, PostToolUse,
// PreInvocation, PostInvocation and Stop. A compaction writes a CHECKPOINT
// step and keeps the steps before it in transcript.jsonl, so the next
// PreInvocation finds it there and the session reads as it stood before it,
// the way Gemini's next BeforeAgent does.

// ReadAntigravityCompactionIfNew is ReadAntigravityCompaction, but returns
// early before parsing the transcript when seen reports the newest CHECKPOINT
// fingerprint has already been processed.
func ReadAntigravityCompactionIfNew(path, conversationID, workspace string, seen func(fingerprint string) bool) (CompactionTranscript, bool, error) {
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
	for i, record := range slices.Backward(records) {
		var step struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(record.line, &step) == nil && step.Type == "CHECKPOINT" {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return CompactionTranscript{}, false, nil
	}
	boundary := records[cut]
	fp := hashCompactionBytes(append(fmt.Appendf(nil, "%d\x00", boundary.off), boundary.line...))
	if seen != nil && seen(fp) {
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
	var calls []TranscriptToolCall
	metricComplete := !truncated
	for _, r := range records[:cut] {
		got, _ := antigravityCompactionToolCalls(r.line, r.off, r.off+int64(len(r.line))+1)
		calls = append(calls, got...)
	}
	calls = dedupeCompactionToolCalls(calls)
	headerLine := bytes.TrimSpace(records[0].line)
	result := CompactionTranscript{
		Session: ss[0], Harness: "antigravity", NativeSessionID: conversationID,
		Workspace: workspace, Path: path,
		// The checkpoint, not the file: later steps append to the file, and
		// the same compaction found again must keep its revision.
		Fingerprint:         fp,
		HeaderFingerprint:   hashCompactionBytes(headerLine),
		BoundaryFingerprint: compactionBoundaryFingerprint(tail, tailOffset, boundary.off),
		SourceSize:          boundary.off, SourceMTime: mtime,
		TailOffset:     tailOffset,
		Truncated:      truncated,
		MetricComplete: metricComplete,
		ToolCalls:      calls,
		tail:           append([]byte(nil), tail...),
	}
	if len(calls) > 0 {
		result.LastToolID = calls[len(calls)-1].ID
	}
	return result, true, nil
}
