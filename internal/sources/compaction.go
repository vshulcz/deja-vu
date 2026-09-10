package sources

// This file deliberately reads a small, stable slice of an active transcript.
// Compaction hooks run on the critical path of an agent conversation, so they
// must never trigger the index's full-store walk merely to recover the most
// recent turn.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	// CompactionTailBytes bounds both memory and synchronous disk I/O for the
	// transcript's changing portion. The separate, short header gives Codex
	// rollouts their native ThreadID without reading their whole history.
	CompactionTailBytes   int64 = 4 << 20
	compactionHeaderBytes int64 = 64 << 10
)

var (
	ErrUnsupportedCompactionTranscript = errors.New("unsupported compaction transcript")
	ErrTranscriptIdentity              = errors.New("compaction transcript identity does not match")
	ErrTranscriptChanging              = errors.New("compaction transcript changed while reading")
	ErrTranscriptLineTooLarge          = errors.New("compaction transcript line exceeds capture bound")
)

// TranscriptToolCall is one native assistant tool invocation. It is not a
// normalized deja Message: several messages can be derived from one invocation
// and using those messages for the compaction-to-edit metric would overcount.
// Offsets refer to the original transcript file, not the bounded temporary
// parse copy.
type TranscriptToolCall struct {
	ID          string    `json:"id,omitempty"`
	Name        string    `json:"name"`
	At          time.Time `json:"at,omitzero"`
	StartOffset int64     `json:"start_offset"`
	EndOffset   int64     `json:"end_offset"`
}

// CompactionTranscript is the bounded, current portion of one known native
// session. Truncated says earlier records were deliberately omitted, never that
// an omitted record was interpreted. TailOffset makes a later metric fail
// closed when the required interval has fallen out of the bounded window.
type CompactionTranscript struct {
	Session           model.Session `json:"session"`
	Harness           string        `json:"harness"`
	NativeSessionID   string        `json:"native_session_id"`
	Workspace         string        `json:"workspace,omitempty"`
	Path              string        `json:"path"`
	Fingerprint       string        `json:"fingerprint"`
	HeaderFingerprint string        `json:"header_fingerprint"`
	// BoundaryFingerprint is a hash of the final captured 4 KiB. It lets a
	// later hook prove that the old end-of-file still occurs at its recorded
	// offset before it measures an append-only interval.
	BoundaryFingerprint string    `json:"boundary_fingerprint,omitempty"`
	SourceSize          int64     `json:"source_size"`
	SourceMTime         time.Time `json:"source_mtime"`
	TailOffset          int64     `json:"tail_offset"`
	Truncated           bool      `json:"truncated"`
	// MetricComplete is false when a retained JSONL record could not be read.
	// Recovery can still be an explicitly truncated best-effort packet, but
	// action measurement must not turn an unknown record into a zero count.
	MetricComplete bool                 `json:"metric_complete"`
	ToolCalls      []TranscriptToolCall `json:"tool_calls,omitempty"`
	LastToolID     string               `json:"last_tool_id,omitempty"`
	tail           []byte
}

// ReadCompactionTranscript captures a regular Claude or Codex JSONL transcript
// without consulting the index. nativeSessionID is mandatory: recovering a
// nearby file with a guessed identity could put another conversation into an
// agent's prompt, which is worse than returning no recovery packet.
func ReadCompactionTranscript(path, nativeSessionID string) (CompactionTranscript, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	info, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, err
	}
	beforeSize, beforeMTime := info.Size(), info.ModTime()
	header, tail, tailOffset, truncated, err := readCompactionParts(path, beforeSize)
	if err != nil {
		return CompactionTranscript{}, err
	}
	// A second stat catches an append or rewrite while a hook was reading. Do
	// not use a partially-observed transcript to make persisted claims.
	after, err := regularCompactionFile(path)
	if err != nil {
		return CompactionTranscript{}, err
	}
	if !os.SameFile(info, after) || after.Size() != beforeSize || !after.ModTime().Equal(beforeMTime) {
		return CompactionTranscript{}, ErrTranscriptChanging
	}

	harness, err := compactionHarness(header, tail)
	if err != nil {
		return CompactionTranscript{}, err
	}
	calls, workspace, metricComplete, err := compactionToolCalls(harness, header, tail, tailOffset, nativeSessionID)
	if err != nil {
		return CompactionTranscript{}, err
	}
	combined := append(append([]byte(nil), header...), tail...)
	session, err := parseCompactionSession(path, harness, workspace, combined)
	if err != nil {
		return CompactionTranscript{}, err
	}
	if session.ID != nativeSessionID {
		return CompactionTranscript{}, fmt.Errorf("%w: got %q, want %q", ErrTranscriptIdentity, session.ID, nativeSessionID)
	}
	result := CompactionTranscript{
		Session:             session,
		Harness:             harness,
		NativeSessionID:     nativeSessionID,
		Workspace:           workspace,
		Path:                path,
		Fingerprint:         compactionFingerprint(beforeSize, beforeMTime, header, tail),
		HeaderFingerprint:   hashCompactionBytes(header),
		BoundaryFingerprint: compactionBoundaryFingerprint(tail, tailOffset, beforeSize),
		SourceSize:          beforeSize,
		SourceMTime:         beforeMTime,
		TailOffset:          tailOffset,
		Truncated:           truncated || !metricComplete,
		MetricComplete:      metricComplete,
		ToolCalls:           calls,
		tail:                append([]byte(nil), tail...),
	}
	if len(calls) > 0 {
		result.LastToolID = calls[len(calls)-1].ID
	}
	return result, nil
}

func regularCompactionFile(path string) (os.FileInfo, error) {
	link, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if link.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: symbolic link", ErrUnsupportedCompactionTranscript)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrUnsupportedCompactionTranscript)
	}
	return info, nil
}

func readCompactionParts(path string, size int64) (header, tail []byte, tailOffset int64, truncated bool, _ error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, false, err
	}
	defer func() { _ = f.Close() }()
	if size <= CompactionTailBytes {
		all, err := io.ReadAll(io.LimitReader(f, CompactionTailBytes+1))
		if err != nil {
			return nil, nil, 0, false, err
		}
		if int64(len(all)) != size {
			return nil, nil, 0, false, ErrTranscriptChanging
		}
		// The complete file was stable across two stats, so a final JSON record
		// without a newline is still a record. Only a bounded *tail* can end in
		// a potentially torn append and needs its final partial line removed.
		return nil, all, 0, false, nil
	}

	headerBuf := make([]byte, compactionHeaderBytes)
	n, err := f.ReadAt(headerBuf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, 0, false, err
	}
	headerBuf = headerBuf[:n]
	i := bytes.LastIndexByte(headerBuf, '\n')
	if i < 0 {
		return nil, nil, 0, false, ErrTranscriptLineTooLarge
	}
	header = append([]byte(nil), headerBuf[:i+1]...)

	tailOffset = size - CompactionTailBytes
	tail = make([]byte, CompactionTailBytes)
	n, err = f.ReadAt(tail, tailOffset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, 0, false, err
	}
	tail = tail[:n]
	needsAlignment := true
	if tailOffset < int64(len(header)) {
		skip := int64(len(header)) - tailOffset
		if skip >= int64(len(tail)) {
			tail = nil
		} else {
			tail = tail[skip:]
			tailOffset += skip
		}
		// The header itself ended at a record boundary, so after removing its
		// overlap the tail starts at the next record. Do not discard that record
		// while trying to align a partial tail read.
		needsAlignment = false
	}
	if needsAlignment {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tailOffset += int64(i + 1)
			tail = tail[i+1:]
		} else if len(tail) > 0 {
			return nil, nil, 0, false, ErrTranscriptLineTooLarge
		}
	}
	tail = trimCompactionTornTail(tail)
	return header, tail, tailOffset, true, nil
}

func trimCompactionTornTail(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[:i+1]
	}
	return nil
}

func compactionHarness(header, tail []byte) (string, error) {
	for _, part := range [][]byte{header, tail} {
		for len(part) > 0 {
			i := bytes.IndexByte(part, '\n')
			end := i + 1
			if i < 0 {
				i, end = len(part), len(part)
			}
			line, rest := bytes.TrimSpace(part[:i]), part[end:]
			part = rest
			var record struct {
				Type      string `json:"type"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			// Codex writes session_meta first. Recognize it before generic
			// session-shaped records, while Claude's file-history/progress
			// preamble is still reliable evidence of a Claude transcript.
			if record.Type == "session_meta" {
				return "codex", nil
			}
			if record.SessionID != "" {
				return "claude", nil
			}
		}
	}
	return "", ErrUnsupportedCompactionTranscript
}

func parseCompactionSession(originalPath, harness, workspace string, data []byte) (model.Session, error) {
	var sessions []model.Session
	var err error
	switch harness {
	case "claude":
		sessions, err = parseClaudeTypedWithScanner(originalPath, compactionByteScanner(data))
	case "codex":
		sessions, err = parseCodexRolloutWithScanner(model.Session{
			Harness: "codex", Project: projectName(filepath.Dir(originalPath)), Path: originalPath,
		}, compactionMapScanner(originalPath, data))
	default:
		return model.Session{}, ErrUnsupportedCompactionTranscript
	}
	if err != nil {
		return model.Session{}, err
	}
	if len(sessions) != 1 || sessions[0].ID == "" {
		return model.Session{}, fmt.Errorf("%w: no parseable native session", ErrUnsupportedCompactionTranscript)
	}
	s := sessions[0]
	s.Path = originalPath
	// The temporary bounded file is only a parser transport. Never let its
	// random /tmp parent become persistent project attribution.
	if workspace != "" {
		s.Project = projectName(workspace)
	} else if harness == "claude" {
		s.Project = claudeProjectName(claudeProjectDir(originalPath))
	} else {
		s.Project = projectName(filepath.Dir(originalPath))
	}
	return s, nil
}

func compactionByteScanner(data []byte) func(func([]byte)) error {
	return func(fn func([]byte)) error {
		for len(data) > 0 {
			i := bytes.IndexByte(data, '\n')
			end := i + 1
			if i < 0 {
				i, end = len(data), len(data)
			}
			line := trimJSONSpace(data[:i])
			data = data[end:]
			if len(line) > 0 {
				fn(line)
			}
		}
		return nil
	}
}

func compactionMapScanner(path string, data []byte) func(func(map[string]any)) error {
	return func(fn func(map[string]any)) error {
		return compactionByteScanner(data)(func(line []byte) {
			var record map[string]any
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.UseNumber()
			if decoder.Decode(&record) != nil {
				diagMalformedLine(path)
				return
			}
			fn(record)
		})
	}
}

func compactionToolCalls(harness string, header, tail []byte, tailOffset int64, nativeSessionID string) ([]TranscriptToolCall, string, bool, error) {
	var calls []TranscriptToolCall
	workspace := ""
	metricComplete := true
	if len(header) > 0 {
		var err error
		var complete bool
		calls, workspace, complete, err = scanCompactionToolLines(harness, header, 0, nativeSessionID, calls, workspace)
		metricComplete = metricComplete && complete
		if err != nil {
			return nil, "", false, err
		}
	}
	var complete bool
	calls, workspace, complete, err := scanCompactionToolLines(harness, tail, tailOffset, nativeSessionID, calls, workspace)
	metricComplete = metricComplete && complete
	if err != nil {
		return nil, "", false, err
	}
	return dedupeCompactionToolCalls(calls), workspace, metricComplete, nil
}

func dedupeCompactionToolCalls(calls []TranscriptToolCall) []TranscriptToolCall {
	seen := make(map[string]bool, len(calls))
	out := calls[:0]
	for _, call := range calls {
		// Synthetic ids are line-local stand-ins for malformed native records;
		// do not merge separate invocations simply because neither had an id.
		if call.ID != "" && !strings.HasPrefix(call.ID, "@") {
			if seen[call.ID] {
				continue
			}
			seen[call.ID] = true
		}
		out = append(out, call)
	}
	return out
}

func scanCompactionToolLines(harness string, data []byte, base int64, nativeSessionID string, calls []TranscriptToolCall, workspace string) ([]TranscriptToolCall, string, bool, error) {
	metricComplete := true
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		endIndex := i + 1
		if i < 0 {
			i, endIndex = len(data), len(data)
		}
		line := bytes.TrimSpace(data[:i])
		start, end := base, base+int64(endIndex)
		base += int64(endIndex)
		data = data[endIndex:]
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			metricComplete = false
			continue
		}
		var got []TranscriptToolCall
		var declaredWorkspace string
		var err error
		switch harness {
		case "claude":
			got, declaredWorkspace, err = claudeCompactionToolCalls(line, start, end, nativeSessionID)
		case "codex":
			got, declaredWorkspace, err = codexCompactionToolCalls(line, start, end, nativeSessionID)
		}
		if err != nil {
			return nil, "", false, err
		}
		if declaredWorkspace != "" {
			if workspace != "" && workspace != declaredWorkspace {
				return nil, "", false, ErrTranscriptIdentity
			}
			workspace = declaredWorkspace
		}
		calls = append(calls, got...)
	}
	return calls, workspace, metricComplete, nil
}

func claudeCompactionToolCalls(line []byte, start, end int64, nativeSessionID string) ([]TranscriptToolCall, string, error) {
	var record struct {
		Type        string          `json:"type"`
		SessionID   string          `json:"sessionId"`
		CWD         string          `json:"cwd"`
		Timestamp   json.RawMessage `json:"timestamp"`
		IsSidechain bool            `json:"isSidechain"`
		AgentID     string          `json:"agentId"`
		Message     *struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil {
		return nil, "", nil
	}
	id := record.SessionID
	if record.IsSidechain && record.AgentID != "" {
		id = record.AgentID
	}
	if id != "" && id != nativeSessionID {
		return nil, "", ErrTranscriptIdentity
	}
	if record.Type != "user" && record.Type != "assistant" {
		return nil, record.CWD, nil
	}
	if record.Type != "assistant" || record.Message == nil || (record.Message.Role != "" && record.Message.Role != "assistant") {
		return nil, record.CWD, nil
	}
	var out []TranscriptToolCall
	var content []json.RawMessage
	if json.Unmarshal(record.Message.Content, &content) != nil {
		return nil, record.CWD, nil
	}
	for n, raw := range content {
		var item struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "tool_use" || item.Name == "" {
			continue
		}
		if item.ID == "" {
			item.ID = fmt.Sprintf("@%d:%d", start, n)
		}
		out = append(out, TranscriptToolCall{ID: item.ID, Name: item.Name, At: claudeTime(record.Timestamp), StartOffset: start, EndOffset: end})
	}
	return out, record.CWD, nil
}

func codexCompactionToolCalls(line []byte, start, end int64, nativeSessionID string) ([]TranscriptToolCall, string, error) {
	var record struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Payload   struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			CWD       string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil {
		return nil, "", nil
	}
	if record.Type == "session_meta" {
		id := record.Payload.ID
		if id == "" {
			id = record.Payload.SessionID
		}
		if id == "" || id != nativeSessionID {
			return nil, "", ErrTranscriptIdentity
		}
		return nil, record.Payload.CWD, nil
	}
	if record.Type != "response_item" || (record.Payload.Type != "function_call" && record.Payload.Type != "custom_tool_call") || record.Payload.Name == "" {
		return nil, "", nil
	}
	id := record.Payload.CallID
	if id == "" {
		id = fmt.Sprintf("@%d", start)
	}
	return []TranscriptToolCall{{ID: id, Name: record.Payload.Name, At: parseTimeAny(rawJSONTime(record.Timestamp)), StartOffset: start, EndOffset: end}}, "", nil
}

func rawJSONTime(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func hashCompactionBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func compactionFingerprint(size int64, mtime time.Time, header, tail []byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00%d\x00", size, mtime.UnixNano())
	_, _ = h.Write(header)
	_, _ = h.Write(tail)
	return hex.EncodeToString(h.Sum(nil))
}

const compactionBoundaryBytes int64 = 4 << 10

func compactionBoundaryFingerprint(tail []byte, tailOffset, at int64) string {
	if at < 0 || tailOffset < 0 || tailOffset+int64(len(tail)) < at {
		return ""
	}
	start := at - compactionBoundaryBytes
	if start < 0 {
		start = 0
	}
	if start < tailOffset {
		return ""
	}
	lo, hi := start-tailOffset, at-tailOffset
	if lo < 0 || hi < lo || hi > int64(len(tail)) {
		return ""
	}
	return hashCompactionBytes(tail[lo:hi])
}

// MatchesCompactionBoundary proves that the bytes ending at capturedSize are
// still present in current's bounded tail. It is deliberately false rather
// than optimistic when that boundary cannot be inspected.
func MatchesCompactionBoundary(current CompactionTranscript, capturedSize int64, capturedBoundaryFingerprint string) bool {
	if capturedBoundaryFingerprint == "" || capturedSize < 0 || current.SourceSize < capturedSize {
		return false
	}
	return compactionBoundaryFingerprint(current.tail, current.TailOffset, capturedSize) == capturedBoundaryFingerprint
}

// CountActionsBeforeEdit counts native calls written after a compaction
// capture and before a known pending explicit edit. It refuses an interval that
// has fallen out of the current tail or whose transcript identity/header does
// not still agree; callers must record that as unmeasured, never as zero.
func CountActionsBeforeEdit(current CompactionTranscript, capturedSize int64, capturedLastToolID, pendingToolID string) (int, bool) {
	if !current.MetricComplete || capturedSize < 0 || current.SourceSize < capturedSize || current.TailOffset > capturedSize || current.NativeSessionID == "" {
		return 0, false
	}
	if capturedLastToolID != "" && current.SourceSize == capturedSize {
		// No bytes can have been written after the boundary. This is a valid
		// zero only when the hook identifies the edit it is about to make.
		return 0, pendingToolID != ""
	}
	counted := 0
	seenPending := false
	for _, call := range current.ToolCalls {
		if call.StartOffset < capturedSize {
			continue
		}
		if pendingToolID != "" && call.ID == pendingToolID {
			seenPending = true
			break
		}
		// The incoming edit may not yet be in the transcript. It is excluded
		// by its hook ID when present; a distinct edit already recorded belongs
		// to the "actions before the next edit" interval and is not counted.
		if IsCompactionEditTool(call.Name) {
			break
		}
		counted++
	}
	if pendingToolID != "" && !seenPending {
		// A hook normally runs before its native record is flushed, so absence
		// is expected. The offset boundary still makes the count reliable.
		return counted, true
	}
	return counted, true
}

// IsCompactionEditTool identifies a native tool whose invocation itself edits
// a file. It intentionally excludes shell commands: whether a shell command
// edits is ambiguous, and counting it would turn a heuristic into the metric's
// boundary. Hook adapters use this same matcher for their pending edit event.
func IsCompactionEditTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "edit", "write", "multiedit", "notebookedit", "apply_patch", "applypatch", "search_replace", "strreplace", "replace", "replace_in_file", "write_file":
		return true
	default:
		return false
	}
}
