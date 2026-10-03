package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Gemini CLI has no hook after a compaction. PreCompress fires on every
// compression attempt, before the token threshold is checked
// (context/chatCompressionService.ts), so it cannot say that one happened.
// The transcript can: the turns are appended as they come and stay in the
// file, and a compaction then writes the new, shorter history over them —
// 0.60 as a {"$set":{"messages":[…]}} snapshot, later builds as
// {"$patch":{"removeIds":[…]}} after the summary turns it inserts. In both
// the oldest turn leaves the history, which a rewind (it cuts the end), tool
// output masking (it keeps every turn) and a resume (it drops only turns it
// never replays) do not do.

// geminiRecord is one transcript line with where it starts.
type geminiRecord struct {
	off  int64
	line []byte
}

// ReadGeminiCompaction finds the newest compaction in a Gemini CLI transcript
// and captures the session as it stood just before it. found is false when
// the transcript holds none.
func ReadGeminiCompaction(path, nativeSessionID string) (CompactionTranscript, bool, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, false, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
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
	if len(records) == 0 || !geminiHeaderFor(records[0].line, nativeSessionID) {
		return CompactionTranscript{}, false, fmt.Errorf("%w: not the gemini session %q", ErrTranscriptIdentity, nativeSessionID)
	}
	cut, at := geminiCompactionBoundary(records)
	if cut < 0 {
		return CompactionTranscript{}, false, nil
	}
	var prefix []byte
	for _, r := range records[:cut] {
		prefix = append(append(prefix, r.line...), '\n')
	}
	ss, err := parseGeminiJSONLWith(path, compactionMapScanner(path, prefix))
	if err != nil {
		return CompactionTranscript{}, false, err
	}
	if len(ss) != 1 || ss[0].ID != nativeSessionID {
		return CompactionTranscript{}, false, fmt.Errorf("%w: no parseable gemini session", ErrUnsupportedCompactionTranscript)
	}
	boundary := records[at]
	return CompactionTranscript{
		Session: ss[0], Harness: "gemini", NativeSessionID: nativeSessionID,
		Workspace: GeminiProjectDir(path), Path: path,
		// The compaction, not the file: later turns append to the file, and
		// the same compaction found again must keep its revision.
		Fingerprint:       hashCompactionBytes(append(fmt.Appendf(nil, "%d\x00", boundary.off), boundary.line...)),
		HeaderFingerprint: hashCompactionBytes(records[0].line),
		SourceSize:        records[cut].off, SourceMTime: mtime,
		TailOffset: tailOffset, Truncated: truncated,
	}, true, nil
}

func splitGeminiRecords(out []geminiRecord, data []byte, base int64) []geminiRecord {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		end := i + 1
		if i < 0 {
			i, end = len(data), len(data)
		}
		if line := bytes.TrimSpace(data[:i]); len(line) > 0 {
			out = append(out, geminiRecord{off: base, line: line})
		}
		base += int64(end)
		data = data[end:]
	}
	return out
}

// geminiHeaderFor reports whether line is the metadata record Gemini opens a
// transcript with, for this session.
func geminiHeaderFor(line []byte, id string) bool {
	var meta struct {
		SessionID   string `json:"sessionId"`
		ProjectHash string `json:"projectHash"`
	}
	return json.Unmarshal(line, &meta) == nil && meta.ProjectHash != "" && meta.SessionID == id
}

// geminiTurn is a turn as a rewrite lists it.
type geminiTurn struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

func (t geminiTurn) isTurn() bool { return t.ID != "" && (t.Type == "user" || t.Type == "gemini") }

// geminiCompactionBoundary returns where the newest compaction starts — the
// index of the first record that is not the old history — and the index of the
// record that rewrote the history, or -1, -1 when there was none.
//
// A compaction is the one rewrite that both drops turns a resume would replay
// and brings in turns never written before: the summary. A resume first
// writes a history of its opening context alone and then the whole history
// back, a rewind cuts the end, and masking only changes what turns say.
func geminiCompactionBoundary(records []geminiRecord) (cut, at int) {
	cut, at = -1, -1
	var order []string
	firstAt := map[string]int{}
	dropped := map[string]bool{}
	note := func(t geminiTurn, i int) bool {
		if _, ok := firstAt[t.ID]; ok {
			return false
		}
		firstAt[t.ID] = i
		dropped[t.ID] = geminiResumeDrops(geminiMessage{ID: t.ID, Type: t.Type, Content: t.Content})
		return true
	}
	// run is where the contiguous turns just before a rewrite start: later
	// builds append the summary turns there, ahead of the $patch.
	run := -1
	for i, r := range records {
		var rec struct {
			geminiTurn
			Set      json.RawMessage `json:"$set"`
			Patch    json.RawMessage `json:"$patch"`
			RewindTo string          `json:"$rewindTo"`
		}
		if json.Unmarshal(r.line, &rec) != nil {
			run = -1
			continue
		}
		switch {
		case rec.RewindTo != "":
			if j := slices.Index(order, rec.RewindTo); j >= 0 {
				order = order[:j]
			}
			run = -1
		case rec.Set != nil || rec.Patch != nil:
			next, turns, ok := geminiRewrite(order, rec.Set, rec.Patch)
			if !ok {
				run = -1
				continue
			}
			lost := false
			for _, id := range order {
				if !dropped[id] && !slices.Contains(next, id) {
					lost = true
					break
				}
			}
			isNew := func(id string) bool {
				first, ok := firstAt[id]
				return !ok || (run >= 0 && first >= run)
			}
			// The summary leads the new history, after the opening context a
			// resume would not replay anyway.
			var inserted []string
			for _, id := range next {
				if isNew(id) {
					inserted = append(inserted, id)
				} else if !dropped[id] {
					break
				}
			}
			if lost && len(inserted) > 0 {
				at, cut = i, i
				// Summary turns written just before the rewrite are new
				// history, not old: the boundary moves back over them.
				for j := run; run >= 0 && j < i; j++ {
					if slices.Contains(inserted, geminiRecordID(records[j].line)) {
						cut = j
						break
					}
				}
			}
			for _, t := range turns {
				note(t, i)
			}
			order = next
			run = -1
		case rec.isTurn():
			if run < 0 {
				run = i
			}
			if note(rec.geminiTurn, i) {
				order = append(order, rec.ID)
			}
		default:
			run = -1
		}
	}
	return cut, at
}

// geminiRewrite reads a history rewrite: the turn ids in their new order and
// the turns it lists. ok is false for a record that does not touch the turns.
func geminiRewrite(order []string, set, patch json.RawMessage) (next []string, turns []geminiTurn, ok bool) {
	if set != nil {
		var s struct {
			Messages []geminiTurn `json:"messages"`
		}
		if json.Unmarshal(set, &s) != nil || s.Messages == nil {
			return nil, nil, false
		}
		for _, t := range s.Messages {
			if t.isTurn() && !slices.Contains(next, t.ID) {
				next = append(next, t.ID)
				turns = append(turns, t)
			}
		}
		return next, turns, true
	}
	var p struct {
		RemoveIDs []string `json:"removeIds"`
		OrderIDs  []string `json:"orderIds"`
	}
	if json.Unmarshal(patch, &p) != nil || (p.RemoveIDs == nil && p.OrderIDs == nil) {
		return nil, nil, false
	}
	if p.OrderIDs != nil {
		return p.OrderIDs, nil, true
	}
	for _, id := range order {
		if !slices.Contains(p.RemoveIDs, id) {
			next = append(next, id)
		}
	}
	return next, nil, true
}

func geminiRecordID(line []byte) string {
	var rec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(line, &rec)
	return rec.ID
}
