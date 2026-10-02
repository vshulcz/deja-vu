package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Reasonix 1.x (npm `reasonix` 1.x, branch main-v2) keeps a session as a
// directory, not a JSONL file:
//
//	<state>/projects/<slug>/sessions-v4/<id>/   CLI, per workspace
//	<state>/sessions-v4/<id>/                   CLI hosts with no workspace (bot, ACP)
//	<state>/desktop-sessions-v5/by-id/<id>/     the desktop app
//
// All three are the same store (internal/session.FilesystemPersistence):
// manifest.json, an optional header.json with the workspace, and events.frames,
// the event log. Payloads over 64 KiB live beside the sessions in
// .content-v1/objects/<aa>/<bb>/<sha256> (internal/sessioncontent/store.go).
//
// events.frames is a run of frames: "RX4F", the compressed and raw sizes as
// big-endian uint32, then one zstd frame holding a JSON record
// (internal/session/v4_codec.go:160-179). Records come in batches —
// batch/begin, batch/event×n, batch/end carrying the SHA-256 of the others —
// and only a finished batch counts: a trailing batch without its end is a
// write still in progress (v4_codec.go:181-186).
const (
	reasonixV4Codec      = "reasonix.session.linear/v4"
	reasonixV4FrameMagic = "RX4F"
	reasonixV4HeaderSize = 12
	reasonixV4MaxFrame   = 8 << 20
)

type reasonixV4Record struct {
	RecordType string    `json:"recordType"`
	CommitID   string    `json:"commitId"`
	EventCount int       `json:"eventCount"`
	CreatedAt  time.Time `json:"createdAt"`
	SHA256     string    `json:"sha256"`
	Event      *struct {
		Kind string `json:"kind"`
		// Payload is []byte on Reasonix's side, so it arrives as base64.
		Payload    []byte `json:"payload"`
		PayloadRef *struct {
			Digest string `json:"digest"`
			Bytes  int64  `json:"bytes"`
		} `json:"payloadRef"`
	} `json:"event"`
}

type reasonixV4Manifest struct {
	Codec       string    `json:"codec"`
	SessionID   string    `json:"sessionId"`
	CreatedAt   time.Time `json:"createdAt"`
	ContentRoot string    `json:"contentRoot"`
	Source      *struct {
		Path string `json:"path"`
	} `json:"source"`
}

func readReasonixV4Manifest(dir string) (reasonixV4Manifest, bool) {
	var m reasonixV4Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, m.Codec == reasonixV4Codec
}

// errReasonixNoZstd is what a frames file says on a machine without the CLI;
// SkipReason names the same cause on the harness row.
var errReasonixNoZstd = errors.New("reasonix: zstd CLI not found")

// readReasonixV4Frames returns the JSON of every complete frame. deja has no
// Go zstd, so the frames go through the zstd CLI — all of them in one process:
// zstd decodes concatenated frames to the concatenation, and the raw sizes in
// the headers cut it back apart. A partial frame at the end is a write in
// progress and is left out. Damage stops the read where it starts, which is
// where Reasonix's own reader stops too (v4_codec.go:331-370).
func readReasonixV4Frames(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var frames [][]byte
	var sizes []int
	var damage error
	for off := 0; off+reasonixV4HeaderSize <= len(data); {
		h := data[off : off+reasonixV4HeaderSize]
		if string(h[:4]) != reasonixV4FrameMagic {
			damage = fmt.Errorf("reasonix: bad frame magic at %d", off)
			break
		}
		c, r := int(binary.BigEndian.Uint32(h[4:8])), int(binary.BigEndian.Uint32(h[8:12]))
		if c <= 0 || c > reasonixV4MaxFrame || r <= 0 || r > reasonixV4MaxFrame {
			damage = fmt.Errorf("reasonix: bad frame sizes at %d", off)
			break
		}
		end := off + reasonixV4HeaderSize + c
		if end > len(data) {
			break
		}
		frames = append(frames, data[off+reasonixV4HeaderSize:end])
		sizes = append(sizes, r)
		off = end
	}
	if len(frames) == 0 {
		return nil, damage
	}
	if !ZstdAvailable() {
		return nil, errReasonixNoZstd
	}
	total := 0
	for _, n := range sizes {
		total += n
	}
	out, err := reasonixZstd(bytes.Join(frames, nil))
	if err != nil || len(out) != total {
		// One bad frame fails the whole batch; decode one at a time to keep
		// what comes before it.
		return reasonixV4FramesOneByOne(frames, sizes)
	}
	raws := make([][]byte, len(sizes))
	for i, n := range sizes {
		raws[i], out = out[:n:n], out[n:]
	}
	return raws, damage
}

func reasonixV4FramesOneByOne(frames [][]byte, sizes []int) ([][]byte, error) {
	var raws [][]byte
	for i, f := range frames {
		raw, err := reasonixZstd(f)
		if err != nil || len(raw) != sizes[i] {
			return raws, fmt.Errorf("reasonix: frame %d does not decode: %v", i, err)
		}
		raws = append(raws, raw)
	}
	return raws, nil
}

func reasonixZstd(in []byte) ([]byte, error) {
	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(in)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("zstd -d: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// reasonixV4Event is one event of a committed batch, with the batch's clock.
type reasonixV4Event struct {
	kind    string
	payload []byte
	at      time.Time
}

// reasonixV4Commits replays the log into the events of its finished batches.
// A batch counts once its end names the same commit, carries the announced
// number of events, and its checksum matches: SHA-256 over each earlier
// record's JSON followed by a zero byte (v4_codec.go:138-150, 283-289).
func reasonixV4Commits(raws [][]byte, content string) []reasonixV4Event {
	var out, pending []reasonixV4Event
	var begin *reasonixV4Record
	var digest = sha256.New()
	for _, raw := range raws {
		var rec reasonixV4Record
		if json.Unmarshal(raw, &rec) != nil {
			return out
		}
		switch rec.RecordType {
		case "batch/begin":
			r := rec
			begin, pending = &r, nil
			digest.Reset()
			digest.Write(raw)
			digest.Write([]byte{0})
		case "batch/event":
			if begin == nil || rec.Event == nil {
				continue
			}
			digest.Write(raw)
			digest.Write([]byte{0})
			ev := reasonixV4Event{kind: rec.Event.Kind, payload: rec.Event.Payload, at: begin.CreatedAt}
			if ref := rec.Event.PayloadRef; ref != nil && len(ev.payload) == 0 {
				ev.payload = readReasonixContent(content, ref.Digest, ref.Bytes)
			}
			pending = append(pending, ev)
		case "batch/end":
			if begin != nil && rec.CommitID == begin.CommitID && rec.EventCount == len(pending) &&
				rec.SHA256 == hex.EncodeToString(digest.Sum(nil)) {
				out = append(out, pending...)
			}
			begin, pending = nil, nil
		}
	}
	return out
}

// readReasonixContent reads one large payload from the content store, and only
// when its bytes are the ones the log names (sessioncontent/store.go:300-356).
func readReasonixContent(root, digest string, size int64) []byte {
	if root == "" || len(digest) != 64 || strings.ContainsAny(digest, `/\.`) {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, "objects", digest[:2], digest[2:4], digest))
	if err != nil || int64(len(b)) != size {
		return nil
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != digest {
		return nil
	}
	return b
}

// reasonixV4ContentRoot is where a session's large payloads live: the
// manifest's contentRoot when it is one of the two Reasonix accepts, else the
// shared store beside the sessions (session/store.go:720-735).
func reasonixV4ContentRoot(dir string, m reasonixV4Manifest) string {
	root := filepath.Join(filepath.Dir(dir), ".content-v1")
	if cr := strings.TrimSpace(m.ContentRoot); cr != "" {
		cand := filepath.Clean(filepath.Join(dir, cr))
		if rel, err := filepath.Rel(dir, cand); err == nil && (rel == ".content-v1" || rel == filepath.Join("..", ".content-v1")) {
			root = cand
		}
	}
	return root
}

// reasonixV4Message is the part of provider.Message deja reads
// (internal/provider/provider.go:46-112).
type reasonixV4Message struct {
	Role          string `json:"role"`
	ID            string `json:"id"`
	Origin        string `json:"origin"`
	Content       any    `json:"content"`
	RawContent    string `json:"raw_content"`
	ToolCalls     any    `json:"tool_calls"`
	ToolCallID    string `json:"tool_call_id"`
	CreatedAt     int64  `json:"createdAt"`
	LocalOnly     bool   `json:"local_only"`
	ToolExecution *struct {
		ExitCode *int `json:"exitCode"`
	} `json:"tool_execution"`

	at time.Time
}

// reasonixV4Transcript is the message list after replaying the events the way
// Reasonix's projection does (internal/session/projection.go:132-330):
// message/complete appends unless the id is already there, message/upsert
// replaces by id, message/retract drops ids, and history/replace and
// legacy/import swap in a whole new list.
type reasonixV4Transcript struct {
	msgs      []*reasonixV4Message
	title     string
	workspace string
}

func (tr *reasonixV4Transcript) index(id string) int {
	if id == "" {
		return -1
	}
	for i, m := range tr.msgs {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func (tr *reasonixV4Transcript) apply(ev reasonixV4Event) {
	switch ev.kind {
	case "message/complete", "message/upsert":
		var body struct {
			Message *reasonixV4Message `json:"message"`
		}
		if json.Unmarshal(ev.payload, &body) != nil || body.Message == nil {
			return
		}
		m := body.Message
		tr.noteWorkspace(m)
		i := tr.index(m.ID)
		switch {
		case i < 0:
			m.at = reasonixMessageTime(m, ev.at)
			tr.msgs = append(tr.msgs, m)
		case ev.kind == "message/upsert":
			m.at = tr.msgs[i].at
			if m.CreatedAt > 0 {
				m.at = time.UnixMilli(m.CreatedAt).UTC()
			}
			tr.msgs[i] = m
		}
	case "message/retract":
		var body struct {
			IDs []string `json:"messageIds"`
		}
		if json.Unmarshal(ev.payload, &body) != nil {
			return
		}
		gone := map[string]bool{}
		for _, id := range body.IDs {
			gone[id] = true
		}
		kept := tr.msgs[:0]
		for _, m := range tr.msgs {
			if !gone[m.ID] || m.ID == "" {
				kept = append(kept, m)
			}
		}
		tr.msgs = kept
	case "history/replace", "legacy/import":
		var body struct {
			Messages []*reasonixV4Message `json:"messages"`
		}
		if json.Unmarshal(ev.payload, &body) != nil || body.Messages == nil {
			return
		}
		prev := tr.msgs
		tr.msgs = nil
		for _, m := range body.Messages {
			if m == nil || tr.index(m.ID) >= 0 {
				continue
			}
			m.at = ev.at
			for _, p := range prev {
				if m.ID != "" && p.ID == m.ID {
					m.at = p.at
				}
			}
			m.at = reasonixMessageTime(m, m.at)
			tr.noteWorkspace(m)
			tr.msgs = append(tr.msgs, m)
		}
	case "session/title":
		var body struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(ev.payload, &body) == nil {
			tr.title = body.Title
		}
	}
}

func reasonixMessageTime(m *reasonixV4Message, fallback time.Time) time.Time {
	if m.CreatedAt > 0 {
		return time.UnixMilli(m.CreatedAt).UTC()
	}
	return fallback
}

// noteWorkspace keeps the workspace the host names in its session-context
// message: `Current workspace: "<root>"`, Go-quoted (boot/boot.go:2283-2288).
// The CLI writes no header.json, so this is where its sessions say where
// they ran.
func (tr *reasonixV4Transcript) noteWorkspace(m *reasonixV4Message) {
	if m.Origin != "host" {
		return
	}
	text, _ := m.Content.(string)
	const marker = "\nCurrent workspace: "
	i := strings.Index(text, marker)
	if i < 0 {
		return
	}
	line := text[i+len(marker):]
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	if ws, err := strconv.Unquote(strings.TrimSpace(line)); err == nil && ws != "" {
		tr.workspace = ws
	}
}

// loadReasonixV4 replays one session directory's committed history.
func loadReasonixV4(framesPath string) (reasonixV4Transcript, reasonixV4Manifest, []reasonixV4Event, error) {
	dir := filepath.Dir(framesPath)
	var tr reasonixV4Transcript
	m, ok := readReasonixV4Manifest(dir)
	if !ok {
		return tr, m, nil, fmt.Errorf("reasonix: %s has no v4 manifest", dir)
	}
	raws, err := readReasonixV4Frames(framesPath)
	events := reasonixV4Commits(raws, reasonixV4ContentRoot(dir, m))
	for _, ev := range events {
		tr.apply(ev)
	}
	return tr, m, events, err
}

// ParseReasonixV4 reads one 1.x session directory through its events.frames.
func ParseReasonixV4(path string) ([]model.Session, error) {
	tr, m, events, err := loadReasonixV4(path)
	dir := filepath.Dir(path)
	id := m.SessionID
	if id == "" {
		id = filepath.Base(dir)
	}
	s := model.Session{Harness: "reasonix", ID: id, Path: path, Title: firstLineTrim(tr.title)}
	if ws := reasonixV4Workspace(path, tr.workspace); ws != "" {
		s.Project = projectName(ws)
	} else if slug := reasonixV4Slug(path); slug != "" {
		s.Project = claudeProjectName(slug)
	}

	start := m.CreatedAt
	if start.IsZero() && len(events) > 0 {
		start = events[0].at
	}
	clock := start.Add(-time.Millisecond)
	commandAt := map[string][]int{}
	ranges := reasonixRangeCalls{}
	for _, msg := range tr.msgs {
		// The host wrote these, not the person: the session-context snapshot
		// (origin "host", provider/message_origin.go) and the local-only
		// records a frontend replays but a model never sees.
		if msg.Origin == "host" || msg.LocalOnly || msg.Role == "system" {
			continue
		}
		ts := msg.at
		if !ts.After(clock) {
			ts = clock.Add(time.Millisecond)
		}
		clock = ts
		text := textFromContent(msg.Content)
		if raw := strings.TrimSpace(msg.RawContent); raw != "" && msg.Role != "assistant" {
			text = raw
		}
		switch msg.Role {
		case "user", "assistant":
			if text != "" {
				s.Touch(ts)
				s.Messages = append(s.Messages, model.Message{Role: msg.Role, Text: text, Time: ts})
			}
			if msg.Role != "assistant" {
				continue
			}
			ranges.note(msg.ToolCalls)
			calls, _ := msg.ToolCalls.([]any)
			for _, c := range calls {
				callID, _ := mapGet(c, "id").(string)
				for _, rec := range reasonixWorkRecords([]any{c}, ts) {
					if rec.Role == RoleCommand && callID != "" {
						commandAt[callID] = append(commandAt[callID], len(s.Messages))
					}
					s.Touch(ts)
					s.Messages = append(s.Messages, rec)
				}
			}
		case "tool":
			for _, rec := range ranges.spans(msg.ToolCallID, text, ts) {
				s.Touch(ts)
				s.Messages = append(s.Messages, rec)
			}
			// A non-zero exit rides on the command, the way opencode's does.
			if ex := msg.ToolExecution; ex != nil && ex.ExitCode != nil && *ex.ExitCode > 0 {
				for _, i := range commandAt[msg.ToolCallID] {
					s.Messages[i].Text += fmt.Sprintf("  → exit %d", *ex.ExitCode)
				}
				delete(commandAt, msg.ToolCallID)
			}
			if text != "" && IndexToolOutput() {
				s.Touch(ts)
				s.Messages = append(s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(text), Time: ts})
			}
		}
	}
	if len(s.Messages) == 0 {
		return nil, err
	}
	s.Touch(start)
	if len(events) > 0 {
		s.Touch(events[len(events)-1].at)
	}
	return []model.Session{s}, err
}

func mapGet(v any, k string) any {
	m, _ := v.(map[string]any)
	return m[k]
}
