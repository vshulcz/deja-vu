package sources

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rxLog writes a 1.x events.frames the way Reasonix's encoder does
// (internal/session/v4_codec.go:59-179): one zstd frame per record behind a
// 12-byte header, records in begin/event/end batches, the end carrying the
// SHA-256 of the records before it. zstd is the CLI, as in the reader.
type rxLog struct {
	t       *testing.T
	dir     string
	buf     bytes.Buffer
	seq     int
	batches int
}

type rxEvent struct {
	kind    string
	payload any
	// external puts the payload in the content store and the log holds a
	// payloadRef, as Reasonix does above 64 KiB.
	external bool
}

func newRxLog(t *testing.T, dir, id string, created time.Time, header map[string]any) *rxLog {
	t.Helper()
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	writeJSONDoc(t, filepath.Join(dir, "manifest.json"), map[string]any{
		"schemaVersion": 4, "codec": reasonixV4Codec, "storageRevision": 3,
		"contentRoot": "../.content-v1", "sessionId": id, "createdAt": created, "writerGeneration": 1,
	})
	if header != nil {
		writeJSONDoc(t, filepath.Join(dir, "header.json"), header)
	}
	return &rxLog{t: t, dir: dir}
}

func (l *rxLog) record(rec map[string]any) []byte {
	l.t.Helper()
	rec["schemaVersion"], rec["codec"] = 4, reasonixV4Codec
	raw, err := json.Marshal(rec)
	if err != nil {
		l.t.Fatal(err)
	}
	cmd := exec.Command("zstd", "-q", "-c")
	cmd.Stdin = bytes.NewReader(raw)
	z, err := cmd.Output()
	if err != nil {
		l.t.Fatalf("zstd: %v", err)
	}
	var h [12]byte
	copy(h[:], reasonixV4FrameMagic)
	binary.BigEndian.PutUint32(h[4:], uint32(len(z)))
	binary.BigEndian.PutUint32(h[8:], uint32(len(raw)))
	l.buf.Write(h[:])
	l.buf.Write(z)
	return raw
}

// batch writes a committed batch; open leaves the end off, as a crash or a
// write still in flight does.
func (l *rxLog) batch(at time.Time, evs ...rxEvent) { l.write(at, true, evs) }
func (l *rxLog) open(at time.Time, evs ...rxEvent)  { l.write(at, false, evs) }

func (l *rxLog) write(at time.Time, commit bool, evs []rxEvent) {
	l.t.Helper()
	l.batches++
	commitID := "commit-" + string(rune('a'+l.batches))
	digest := sha256.New()
	add := func(raw []byte) { digest.Write(raw); digest.Write([]byte{0}) }
	add(l.record(map[string]any{
		"recordType": "batch/begin", "commitId": commitID, "operationId": "op-" + commitID, "operationHash": "x",
		"firstSeq": l.seq + 1, "eventCount": len(evs), "writerGeneration": 1, "createdAt": at,
	}))
	for _, ev := range evs {
		l.seq++
		body, err := json.Marshal(ev.payload)
		if err != nil {
			l.t.Fatal(err)
		}
		event := map[string]any{"id": "ev" + string(rune('a'+l.seq)), "seq": l.seq, "kind": ev.kind}
		if ev.external {
			sum := sha256.Sum256(body)
			d := hex.EncodeToString(sum[:])
			writeReasonixFile(l.t, filepath.Join(filepath.Dir(l.dir), ".content-v1", "objects", d[:2], d[2:4], d), string(body))
			event["payloadRef"] = map[string]any{"digest": d, "bytes": len(body), "mediaType": "application/json"}
		} else {
			event["payload"] = body // []byte marshals as base64, as Reasonix's does
		}
		add(l.record(map[string]any{"recordType": "batch/event", "event": event}))
	}
	if commit {
		l.record(map[string]any{
			"recordType": "batch/end", "commitId": commitID, "firstSeq": l.seq - len(evs) + 1,
			"eventCount": len(evs), "sha256": hex.EncodeToString(digest.Sum(nil)),
		})
	}
}

func (l *rxLog) save() string {
	l.t.Helper()
	p := filepath.Join(l.dir, "events.frames")
	writeReasonixFile(l.t, p, l.buf.String())
	return p
}

func rxMsg(m map[string]any) rxEvent {
	return rxEvent{kind: "message/complete", payload: map[string]any{"message": m}}
}

const rxSessionContext = "<session-context version=\"1\">\n## Environment\n\n- OS: linux/amd64\n\n## Workspace\n\nCurrent workspace: \"/workspace/demo\"\n</session-context>"

func rxRoles(msgs []modelMessage) map[string][]string {
	out := map[string][]string{}
	for _, m := range msgs {
		out[m.Role] = append(out[m.Role], m.Text)
	}
	return out
}

type modelMessage = struct {
	Role, Text string
	Time       time.Time
}

// The CLI's store: a session directory under projects/<slug>/sessions-v4,
// no header.json, the workspace named only in the host's session-context
// message, and one batch per step of a turn.
func TestParseReasonixV4Session(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	id := "839559938275e9a3ebde5a804aa24edd"
	dir := filepath.Join(root, "projects", "-workspace-demo", "sessions-v4", id)
	t0 := time.Date(2026, 9, 26, 22, 24, 59, 0, time.UTC)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	l := newRxLog(t, dir, id, t0, nil)
	l.batch(at(10),
		rxMsg(map[string]any{"role": "system", "id": "m0", "content": "You are Reasonix, a coding agent."}),
		rxEvent{kind: "session/config", payload: map[string]any{"modelRef": "deepseek/deepseek-chat"}},
	)
	l.batch(at(20), rxEvent{kind: "turn/start", payload: map[string]any{"status": "in_progress"}})
	l.batch(at(30),
		rxMsg(map[string]any{"role": "user", "id": "m1", "origin": "host", "content": rxSessionContext}),
		rxMsg(map[string]any{"role": "user", "id": "m2", "origin": "user", "content": "go build fails in the relay package", "raw_content": "go build fails in the relay package", "createdAt": at(30).UnixMilli()}),
	)
	l.batch(at(40), rxMsg(map[string]any{"role": "assistant", "id": "m3", "content": "draft that gets replaced"}))
	l.batch(at(50), rxEvent{kind: "message/upsert", payload: map[string]any{"message": map[string]any{
		"role": "assistant", "id": "m3", "content": "Building it to see the error.",
		"tool_calls": []any{toolCall("call_1", "bash", map[string]any{"command": "go build ./relay"})},
	}}})
	l.batch(at(60), rxMsg(map[string]any{"role": "tool", "id": "m4", "tool_call_id": "call_1", "name": "bash",
		"content":        "error: exit status 1\nrelay/queue.go:12: undefined: frame",
		"tool_execution": map[string]any{"kind": "shell", "state": "failed", "exitCode": 1}}))
	l.batch(at(65), rxMsg(map[string]any{"role": "assistant", "id": "m5", "content": "an interrupted partial answer"}))
	l.batch(at(66), rxEvent{kind: "message/retract", payload: map[string]any{"messageIds": []string{"m5"}, "reason": "interrupted-turn-cleanup"}})
	l.batch(at(67), rxMsg(map[string]any{"role": "tool", "id": "m6", "local_only": true, "content": "steer the host kept"}))
	l.batch(at(70), rxMsg(map[string]any{"role": "assistant", "id": "m7", "content": "queue.go uses frame before it is declared."}))
	l.batch(at(80), rxEvent{kind: "session/title", payload: map[string]any{"title": "relay build error"}})
	l.open(at(90), rxMsg(map[string]any{"role": "user", "id": "m8", "origin": "user", "content": "a turn still being written"}))
	path := l.save()

	if !IsReasonixSession(path) || !slices.Contains(ReasonixSessionFiles(), path) {
		t.Fatalf("events.frames is not listed: %v", ReasonixSessionFiles())
	}
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.ID != id || s.Title != "relay build error" || s.Project != "workspace/demo" {
		t.Errorf("id/title/project = %q/%q/%q", s.ID, s.Title, s.Project)
	}
	var msgs []modelMessage
	for _, m := range s.Messages {
		msgs = append(msgs, modelMessage{m.Role, m.Text, m.Time})
	}
	by := rxRoles(msgs)
	if got := by["user"]; len(got) != 1 || got[0] != "go build fails in the relay package" {
		t.Errorf("user = %q, want the typed prompt alone (host context and the open batch left out)", got)
	}
	if got := by["assistant"]; strings.Join(got, "|") != "Building it to see the error.|queue.go uses frame before it is declared." {
		t.Errorf("assistant = %q, want the upserted text and no retracted one", got)
	}
	if got := by[RoleCommand]; len(got) != 1 || got[0] != "$ go build ./relay  → exit 1" {
		t.Errorf("commands = %q, want the call with its exit", got)
	}
	if got := by[RoleToolOutput]; len(got) != 1 || !strings.Contains(got[0], "undefined: frame") {
		t.Errorf("tool output = %q", got)
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "session-context") || strings.Contains(m.Text, "You are Reasonix") || strings.Contains(m.Text, "steer the host") {
			t.Errorf("%q was indexed as something someone said", m.Text)
		}
	}
	if !msgs[0].Time.Equal(at(30)) {
		t.Errorf("first turn at %v, want its createdAt", msgs[0].Time)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Time.Before(msgs[i-1].Time) {
			t.Fatalf("record %d goes back in time", i)
		}
	}
	if !s.Started.Equal(t0) || !s.Updated.Equal(at(80)) {
		t.Errorf("started/updated = %v/%v, want the manifest and the last commit", s.Started, s.Updated)
	}
	// The context's workspace has the store's slug, so --resume finds the
	// session from there.
	if ws := ReasonixWorkspace(path); filepath.Separator == '/' && ws != "/workspace/demo" {
		t.Errorf("resume dir = %q", ws)
	}
}

// A batch whose checksum does not match is not a commit, and a damaged frame
// ends the read.
func TestReasonixV4SkipsBadBatches(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "sessions-v4", "s1")
	l := newRxLog(t, dir, "s1", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), nil)
	l.batch(time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u1", "content": "kept"}))
	l.open(time.Date(2026, 9, 1, 0, 0, 2, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u2", "content": "tampered"}))
	l.record(map[string]any{"recordType": "batch/end", "commitId": "commit-c", "eventCount": 1, "sha256": strings.Repeat("0", 64)})
	l.batch(time.Date(2026, 9, 1, 0, 0, 3, 0, time.UTC), rxMsg(map[string]any{"role": "user", "id": "u3", "content": "after the bad batch"}))
	l.buf.WriteString("RX4Fgarbage-that-is-no-frame")
	path := l.save()
	ss, _ := ParseReasonixFile(path)
	if len(ss) != 1 || len(ss[0].Messages) != 2 || ss[0].Messages[0].Text != "kept" || ss[0].Messages[1].Text != "after the bad batch" {
		t.Fatalf("sessions = %+v, want the two batches whose checksums hold", ss)
	}
}

// history/replace and legacy/import swap the whole list; a message that
// survives keeps its clock. A payload over 64 KiB is read from .content-v1.
func TestReasonixV4ReplaceAndPayloadRef(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	dir := filepath.Join(root, "projects", "-workspace-demo", "sessions-v4", "s2")
	t0 := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	l := newRxLog(t, dir, "s2", t0, nil)
	l.batch(t0.Add(time.Second),
		rxMsg(map[string]any{"role": "user", "id": "a", "content": "first question"}),
		rxMsg(map[string]any{"role": "assistant", "id": "b", "content": "dropped by the replace"}),
	)
	big := strings.Repeat("stack frame ", 7000)
	l.batch(t0.Add(2*time.Second), rxEvent{kind: "history/replace", external: true, payload: map[string]any{
		"reason": "fresh-history-adopt",
		"messages": []any{
			map[string]any{"role": "user", "id": "a", "content": "first question"},
			map[string]any{"role": "assistant", "id": "c", "content": big},
		},
	}})
	path := l.save()
	ss, err := ParseReasonixFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d", err, len(ss))
	}
	m := ss[0].Messages
	if len(m) != 2 || m[0].Text != "first question" || !strings.HasPrefix(m[1].Text, "stack frame stack frame") {
		t.Fatalf("messages = %d (%.40q), want the replaced list with the external payload", len(m), m)
	}
	if !m[0].Time.Equal(t0.Add(time.Second)) || !m[1].Time.Equal(t0.Add(2*time.Second)) {
		t.Errorf("times = %v, %v", m[0].Time, m[1].Time)
	}
}

// The desktop's store is by-id; its header.json names the workspace, and the
// registry does when there is no header.
func TestReasonixV4Desktop(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	store := filepath.Join(root, "desktop-sessions-v5", "by-id")
	t0 := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	l := newRxLog(t, filepath.Join(store, "desktop-a1"), "desktop-a1", t0, map[string]any{"schemaVersion": 1, "sessionId": "desktop-a1", "cwd": "/workspace/app", "origin": "new"})
	l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "origin": "user", "content": "why is the sidebar blank"}))
	withHeader := l.save()
	l = newRxLog(t, filepath.Join(store, "desktop-b2"), "desktop-b2", t0, nil)
	l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "origin": "user", "content": "rename the tab"}))
	noHeader := l.save()
	writeJSONDoc(t, filepath.Join(root, "desktop", "workspace-state-v1.json"), map[string]any{
		"version": 1, "workspaces": map[string]any{"project-x": map[string]any{"id": "project-x", "root": "/workspace/site", "sessionIds": []string{"desktop-b2"}}},
	})
	for p, want := range map[string]string{withHeader: "workspace/app", noHeader: "workspace/site"} {
		ss, err := ParseReasonixFile(p)
		if err != nil || len(ss) != 1 || ss[0].Project != want {
			t.Errorf("%s: %v %+v, want project %q", filepath.Base(filepath.Dir(p)), err, ss, want)
		}
		if ReasonixStore(p) != "desktop" || !IsReasonixSession(p) {
			t.Errorf("%s is not a desktop session", p)
		}
	}
}

// Reasonix copies without deleting. deja reads one copy of each session: the
// desktop's over the CLI's, the CLI's v4 over JSONL — by id, by the source a
// migration names in its manifest, and by the desktop's source mappings. A
// fork names its parent as its source too, and the parent is still read.
func TestReasonixStoresDedupe(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	t0 := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	proj := filepath.Join(root, "projects", "-workspace-demo")
	jsonl := func(name string) string {
		p := filepath.Join(proj, "sessions", name+".jsonl")
		writeJSONLines(t, p, map[string]any{"role": "user", "content": name})
		return p
	}
	v4 := func(store, id string, manifest map[string]any) string {
		l := newRxLog(t, filepath.Join(store, id), id, t0, nil)
		l.batch(t0, rxMsg(map[string]any{"role": "user", "id": "u", "content": id}))
		p := l.save()
		if manifest != nil {
			manifest["codec"], manifest["sessionId"] = reasonixV4Codec, id
			writeJSONDoc(t, filepath.Join(store, id, "manifest.json"), manifest)
		}
		return p
	}
	cli := filepath.Join(proj, "sessions-v4")
	byID := filepath.Join(root, "desktop-sessions-v5", "by-id")

	mirrored := jsonl("20260904-mirror")
	mirror := v4(cli, "20260904-mirror", nil)
	migratedSrc := jsonl("20260904-migrated")
	migrated := v4(cli, "0f0f0f0f", map[string]any{"source": map[string]any{"path": migratedSrc}})
	plain := jsonl("20260904-plain")
	parent := v4(cli, "aaaa1111", nil)
	fork := v4(cli, "bbbb2222", map[string]any{"inheritedEventCount": 1, "source": map[string]any{"path": filepath.Dir(parent)}})
	imported := v4(cli, "cccc3333", nil)
	copied := v4(cli, "dddd4444", nil)
	desktop := v4(byID, "desktop-imported", nil)
	sameID := v4(byID, "dddd4444", nil)
	writeJSONDoc(t, filepath.Join(root, "desktop", "workspace-state-v1.json"), map[string]any{
		"sourceMappings": map[string]any{"k": map[string]any{"path": filepath.Dir(imported), "sessionId": "desktop-imported"}},
	})

	got := ReasonixSessionFiles()
	slices.Sort(got)
	want := []string{mirror, migrated, plain, parent, fork, desktop, sameID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("session files =\n%v\nwant\n%v", got, want)
	}
	for _, p := range []string{mirrored, migratedSrc, imported, copied} {
		if !slices.Contains(ReasonixSidecarFiles(), p) {
			t.Errorf("%s is neither read nor placed", p)
		}
	}
}

// Without zstd a frames file cannot be read, and the harness row says why —
// but only on a machine that has one.
func TestReasonixV4WithoutZstd(t *testing.T) {
	root := t.TempDir()
	clearReasonixEnv(t, root)
	t.Setenv("DEJA_REASONIX_ROOT", root)
	writeJSONLines(t, filepath.Join(root, "sessions", "plain.jsonl"), map[string]any{"role": "user", "content": "hi"})
	t.Setenv("PATH", t.TempDir())
	if r := SkipReason("reasonix"); r != "" {
		t.Errorf("JSONL-only store: skip reason %q, want none", r)
	}
	dir := filepath.Join(root, "sessions-v4", "s3")
	writeJSONDoc(t, filepath.Join(dir, "manifest.json"), map[string]any{"codec": reasonixV4Codec, "sessionId": "s3"})
	var h [12]byte
	copy(h[:], reasonixV4FrameMagic)
	binary.BigEndian.PutUint32(h[4:], 4)
	binary.BigEndian.PutUint32(h[8:], 4)
	writeReasonixFile(t, filepath.Join(dir, "events.frames"), string(h[:])+"zstd")
	if r := SkipReason("reasonix"); r != "zstd CLI not found" {
		t.Errorf("skip reason = %q", r)
	}
	if _, err := ParseReasonixFile(filepath.Join(dir, "events.frames")); !errors.Is(err, errReasonixNoZstd) {
		t.Errorf("parse error = %v, want the missing tool named", err)
	}
}

// Reasonix's slug: separators and colons to dashes, and a cut with a hash
// past 255 bytes (config/paths.go:567-597).
func TestReasonixWorkspaceSlug(t *testing.T) {
	if got := reasonixWorkspaceSlug("/tmp/rxstand/work"); filepath.Separator == '/' && got != "-tmp-rxstand-work" {
		t.Errorf("slug = %q", got)
	}
	long := reasonixWorkspaceSlug("/" + strings.Repeat("a", 300))
	if len(long) != 255 || !strings.HasPrefix(long, "-aaa") {
		t.Errorf("long slug = %d bytes %q", len(long), long[len(long)-20:])
	}
}
