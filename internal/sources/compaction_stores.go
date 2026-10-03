package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ReadCompactionStore captures a session that lives in a database rather than
// in a transcript file: opencode and Kilo CLI keep every message in SQLite and
// only filter the compacted ones out of what they send the model, so the
// session read here by its native id still holds the turns about to be
// summarised. The store is the one deja indexes; nothing else is consulted.
func ReadCompactionStore(harness, nativeSessionID string) (CompactionTranscript, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	var db string
	switch harness {
	case "opencode":
		db = OpencodeDB()
	case "kilocode":
		db = KiloDB()
	default:
		return CompactionTranscript{}, ErrUnsupportedCompactionTranscript
	}
	info, err := regularCompactionFile(db)
	if err != nil {
		return CompactionTranscript{}, err
	}
	where := fmt.Sprintf(" and s.id = '%s'", sqlEscape(nativeSessionID))
	ss, err := parseOpencodeSchemaDB(harness, db, where, 0)
	if err != nil {
		return CompactionTranscript{}, err
	}
	if len(ss) != 1 || ss[0].ID != nativeSessionID {
		return CompactionTranscript{}, fmt.Errorf("%w: no session %q in %s", ErrTranscriptIdentity, nativeSessionID, harness)
	}
	s := ss[0]
	// The reader keeps the session's directory as its path; that is the
	// workspace the hook has to match.
	workspace := s.Path
	return CompactionTranscript{
		Session: s, Harness: harness, NativeSessionID: nativeSessionID, Workspace: workspace,
		Path: db, Fingerprint: sessionFingerprint(s), SourceSize: info.Size(), SourceMTime: info.ModTime(),
		// The store gives no byte offsets, so there is nothing for the
		// compaction-to-edit metric to count against.
		MetricComplete: false,
	}, nil
}

// ReadCompactionMessages captures the turns a host hands over in process.
// Hermes passes its memory provider the message list it is about to compress,
// in the OpenAI chat shape it keeps in state.db; the rows go through the same
// reader as the store's, so commands, exit codes and results come out alike.
func ReadCompactionMessages(harness, nativeSessionID, workspace string, raw json.RawMessage) (CompactionTranscript, error) {
	if strings.TrimSpace(nativeSessionID) == "" {
		return CompactionTranscript{}, fmt.Errorf("%w: missing session id", ErrTranscriptIdentity)
	}
	if harness != "hermes" {
		return CompactionTranscript{}, ErrUnsupportedCompactionTranscript
	}
	var msgs []map[string]any
	if err := json.Unmarshal(raw, &msgs); err != nil || len(msgs) == 0 {
		return CompactionTranscript{}, errors.Join(ErrUnsupportedCompactionTranscript, err)
	}
	now := time.Now().UTC()
	h := newHermesSession(model.Session{Harness: harness, ID: nativeSessionID, Project: projectName(workspace), Path: workspace})
	for _, m := range msgs {
		h.row(hermesMessageRow(m, now))
	}
	s := h.done()
	if len(s.Messages) == 0 {
		return CompactionTranscript{}, fmt.Errorf("%w: no readable messages", ErrUnsupportedCompactionTranscript)
	}
	return CompactionTranscript{
		Session: s, Harness: harness, NativeSessionID: nativeSessionID, Workspace: workspace,
		Fingerprint: sessionFingerprint(s), SourceMTime: now,
	}, nil
}

// hermesMessageRow puts an in-memory message into the row shape state.db
// stores: content as text, tool_calls as the JSON Hermes would have written.
func hermesMessageRow(m map[string]any, t time.Time) map[string]any {
	r := map[string]any{"role": m["role"], "tool_call_id": m["tool_call_id"], "timestamp": float64(t.Unix())}
	switch c := m["content"].(type) {
	case string:
		r["content"] = c
	case []any:
		var parts []string
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok {
				if txt, _ := pm["text"].(string); txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		r["content"] = strings.Join(parts, "\n")
	}
	if calls, ok := m["tool_calls"]; ok && calls != nil {
		if b, err := json.Marshal(calls); err == nil {
			r["tool_calls"] = string(b)
		}
	}
	if name, _ := m["name"].(string); name != "" {
		r["tool_name"] = name
	} else if name, _ := m["tool_name"].(string); name != "" {
		r["tool_name"] = name
	}
	return r
}

// sessionFingerprint identifies what a store-backed capture read, so the same
// state captured twice keeps its revision.
func sessionFingerprint(s model.Session) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%d\x00", s.ID, len(s.Messages))
	for _, m := range s.Messages {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", m.Role, m.Text)
	}
	return hex.EncodeToString(h.Sum(nil))
}
