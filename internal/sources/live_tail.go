package sources

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// LiveTranscriptTailBytes bounds live prompt-hook transcript reads so a
// multi-MiB session only touches its metadata header and most recent tail.
const LiveTranscriptTailBytes int64 = 256 << 10

func liveTailSupportsHarness(harness string) bool {
	return canReadCompactionTailByHarness(harness) || harness == "copilot" || harness == "qwen" || harness == "gigacode"
}

// ReadLiveTranscriptTailMessages reads recent user and assistant turns from an
// active transcript in a single pass over at most its bounded header and
// LiveTranscriptTailBytes tail, skipping SHA-256 hashing, double-stat retries,
// and tool-call interval scans.
func ReadLiveTranscriptTailMessages(harness, transcriptPath, sessionID string) ([]model.Message, bool) {
	hName := strings.ToLower(strings.TrimSpace(harness))
	tPath := strings.TrimSpace(transcriptPath)
	sid := strings.TrimSpace(sessionID)
	if hName == "trae" || (hName != "" && !liveTailSupportsHarness(hName)) {
		return nil, false
	}

	var paths []string
	switch {
	case tPath != "":
		paths = []string{tPath}
	case sid != "" && hName != "":
		cs := compactionCandidatesByID(hName, sid)
		paths = make([]string, 0, len(cs))
		for _, c := range cs {
			paths = append(paths, c.path)
		}
	default:
		return nil, false
	}

	for _, path := range paths {
		if msgs, ok := readLiveTailFile(hName, path, sid); ok {
			return msgs, true
		}
	}
	return nil, false
}

func readLiveTailFile(expectedHarness, path, sessionID string) ([]model.Message, bool) {
	link, err := os.Lstat(path)
	if err != nil || link.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	size := info.Size()
	if size <= 0 {
		return nil, false
	}

	var header, tail []byte
	if size <= LiveTranscriptTailBytes {
		all, readErr := io.ReadAll(io.LimitReader(f, LiveTranscriptTailBytes))
		if readErr != nil || len(all) == 0 {
			return nil, false
		}
		tail = all
	} else {
		header, err = readBoundedTranscriptHeader(f, size)
		if err != nil {
			return nil, false
		}
		tailOffset := size - LiveTranscriptTailBytes
		tailBuf := make([]byte, LiveTranscriptTailBytes)
		n, readErr := f.ReadAt(tailBuf, tailOffset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false
		}
		tail = tailBuf[:n]
		needsAlignment := true
		if tailOffset < int64(len(header)) {
			skip := int64(len(header)) - tailOffset
			if skip >= int64(len(tail)) {
				tail = nil
			} else {
				tail = tail[skip:]
			}
			needsAlignment = false
		}
		if needsAlignment {
			if i := bytes.IndexByte(tail, '\n'); i >= 0 {
				tail = tail[i+1:]
			} else {
				tail = nil
			}
		}
		tail = trimCompactionTornTail(tail)
	}

	detected, err := compactionHarness(header, tail)
	detected = refineClaudeShapedHarness(path, detected)
	if errors.Is(err, ErrUnsupportedCompactionTranscript) && (qwenShapedHead(header) || qwenShapedHead(tail)) {
		detected, err = "qwen", nil
	}
	detected = refineQwenShapedHarness(path, detected)
	if err != nil || detected == "" {
		return nil, false
	}
	if expectedHarness != "" && !liveTailHarnessMatches(expectedHarness, detected) {
		return nil, false
	}

	combined := make([]byte, len(header)+len(tail))
	copy(combined, header)
	copy(combined[len(header):], tail)

	sess, err := parseCompactionSession(path, detected, "", combined)
	if err != nil {
		return nil, false
	}
	if sessionID != "" && sess.ID != sessionID {
		return nil, false
	}

	msgs := make([]model.Message, 0, len(sess.Messages))
	for _, m := range sess.Messages {
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Text) != "" {
			msgs = append(msgs, model.Message{Role: m.Role, Text: m.Text, Time: m.Time})
		}
	}
	if len(msgs) == 0 {
		return nil, false
	}
	return msgs, true
}

func liveTailHarnessMatches(expected, detected string) bool {
	if expected == detected {
		return true
	}
	return expected == "claude" && (detected == "qoder" || detected == "cherrystudio")
}

// CodexSessionHead resolves a Codex rollout by id and reads only its bounded
// head for lineage resolution without walking the whole session tree.
func CodexSessionHead(id string) (model.Session, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.Session{}, false
	}
	for _, c := range compactionCandidatesByID("codex", id) {
		if s, err := transcriptHead(c.path, id, "codex"); err == nil {
			return s, true
		}
	}
	return model.Session{}, false
}
