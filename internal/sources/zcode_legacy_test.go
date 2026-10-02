package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Before the current runtime ZCode kept each conversation as
// ~/.zcode/v2/sessions/<workspaceHash>/<taskId>.json, {meta, messages}, the
// shape the runtime's own restore-legacy-sessions skill scans. Those stay on
// disk until restored by hand, and deja read neither directory (#4432).
func TestZCodeReadsLegacySessionSnapshots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(home, "absent"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(home, "absent.sqlite"))
	dir := filepath.Join(home, ".zcode", "v2", "sessions", "5f0c1e9a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := `{"meta":{"taskId":"task-1","acpSessionId":"acp-9","workspacePath":"/private/tmp/proj","provider":"glm","title":"fix the retry loop","createdAt":1790000000000,"updatedAt":1790000060000},` +
		`"messages":[{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790000000000},` +
		`{"role":"assistant","content":"capped it at five attempts with a 120 second backoff","timestamp":1790000060000}]}`
	live := filepath.Join(dir, "task-1.json")
	gone := filepath.Join(dir, "task-2.deleted.json")
	for p, body := range map[string]string{live: snap, gone: snap} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := ZCodeSessionFiles()
	if len(files) != 1 || files[0] != live {
		t.Fatalf("ZCodeSessionFiles = %v, want only %s: ZCode skips .deleted.json", files, live)
	}
	ss := LoadZCode()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one snapshot", len(ss))
	}
	s := ss[0]
	// acpSessionId || taskId, the id the restore skill gives the session.
	if s.ID != "acp-9" || s.Harness != "zcode" || s.Project != "tmp/proj" || s.Title != "fix the retry loop" {
		t.Errorf("session = %s %s %q %q, want zcode acp-9 in tmp/proj titled from meta", s.Harness, s.ID, s.Project, s.Title)
	}
	if len(s.Messages) != 2 || s.Messages[1].Role != "assistant" || s.Messages[1].Time.IsZero() {
		t.Errorf("messages = %+v", s.Messages)
	}

	// Restored into the CLI database, the same conversation is read from
	// there, and the snapshot is not a second copy of it.
	if !SQLite3Available() {
		return
	}
	db := filepath.Join(home, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	seed := `create table session (id text primary key, directory text, time_created integer, time_updated integer);
create table message (id text primary key, session_id text, time_created integer, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('acp-9', '/private/tmp/proj', 1790000000000, 1790000060000);
insert into message values ('m1', 'acp-9', 1790000000000, '{"role":"user","time":{"created":1790000000000}}');
insert into part values ('p1', 'm1', '{"type":"text","text":"fix the retry loop, it never stops"}');`
	if out, err := exec.Command("sqlite3", db, seed).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	n := 0
	for _, s := range LoadZCode() {
		if s.ID == "acp-9" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("acp-9 read %d times once restored, want once", n)
	}
}

// The fingerprint asks every snapshot for its id on every pass once the CLI
// database exists, so the id comes off the meta at the head of the file, not
// from decoding a conversation that can run to megabytes (#4448).
func TestZCodeSnapshotIDIsReadOffTheMeta(t *testing.T) {
	dir := t.TempDir()
	head := filepath.Join(dir, "task-1.json")
	if err := os.WriteFile(head, []byte(`{"meta":{"taskId":"task-1","acpSessionId":"acp-1"},"messages":[{"role":"user","content":"fix the re`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := zcodeLegacyID(head); got != "acp-1" {
		t.Errorf("meta first: id %q, want acp-1", got)
	}
	tail := filepath.Join(dir, "task-2.json")
	if err := os.WriteFile(tail, []byte(`{"messages":[{"role":"user","content":"fix it"}],"Meta":{"taskId":"task-2"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := zcodeLegacyID(tail); got != "task-2" {
		t.Errorf("meta last: id %q, want task-2", got)
	}
}

// A snapshot with no meta is read under its file name, and one that is not a
// JSON object, or breaks off before its meta, has no id.
func TestZCodeSnapshotIDWithoutAReadableMeta(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct{ body, want string }{
		"task-3.json": {`{"messages":[{"role":"user","content":"fix it"}],"title":"x"}`, "task-3"},
		"task-4.json": {`[{"meta":{"taskId":"task-4"}}]`, ""},
		"task-5.json": {`{"messages":[{"role":"user","content":"fix`, ""},
		"task-6.json": {`{"messages"`, ""},
		"task-7.json": {``, ""},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := zcodeLegacyID(p); got != c.want {
			t.Errorf("%s: id %q, want %q", name, got, c.want)
		}
	}
	if got := zcodeLegacyID(filepath.Join(dir, "gone.json")); got != "" {
		t.Errorf("missing snapshot: id %q, want none", got)
	}
}

// The fingerprint says whether the CLI database holds the session a snapshot
// was restored as, so restoring one re-reads it and the reader drops it
// (#4448). The id is cached per file state: a rewrite is decoded again.
func TestZCodeLegacySidecarFollowsTheRestore(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "task-1.json")
	other := filepath.Join(dir, "task-2.json")
	for p, body := range map[string]string{
		snap:  `{"meta":{"taskId":"task-1","acpSessionId":"acp-9"},"messages":[]}`,
		other: `{"meta":{"taskId":"task-2"},"messages":[]}`,
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(dir, "db.sqlite")
	t.Setenv("DEJA_ZCODE_DB", db)
	if size, stamp := zcodeLegacySidecar(snap); size != 0 || stamp != 0 {
		t.Errorf("no database = %d/%d, want nothing", size, stamp)
	}
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not available")
	}
	if out, err := exec.Command("sqlite3", db, `create table session (id text primary key); insert into session values ('acp-9');`).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 1 || stamp != 1 {
		t.Errorf("restored snapshot = %d/%d, want 1/1", size, stamp)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 1 || stamp != 1 {
		t.Errorf("restored snapshot read again = %d/%d, want 1/1", size, stamp)
	}
	if size, stamp := zcodeLegacySidecar(other); size != 0 || stamp != 0 {
		t.Errorf("snapshot not restored = %d/%d, want nothing", size, stamp)
	}
	if err := os.WriteFile(snap, []byte(`{"meta":{"taskId":"task-1","acpSessionId":"acp-10"},"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(snap, later, later); err != nil {
		t.Fatal(err)
	}
	if size, stamp := zcodeLegacySidecar(snap); size != 0 || stamp != 0 {
		t.Errorf("snapshot rewritten under a new id = %d/%d, want nothing", size, stamp)
	}
}
