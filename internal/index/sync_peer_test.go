package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

func syncPeerIndex(t *testing.T, msgs ...string) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for i, m := range msgs {
		body += m
		_ = i
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	// Tombstones live outside the cache, so without this every test in the
	// package shares one list and a Forget here leaks into the next test.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, tmp
}

func msgLine(ts, text string) string {
	return `{"type":"user","sessionId":"s1","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// What one peer has already received says nothing about what another peer
// has: a global watermark silently partitions history across machines.
func TestExportWatermarksAreScopedPerPeer(t *testing.T) {
	skipWindowsPortability(t)
	dir, tmp := syncPeerIndex(t, msgLine("2026-01-02T03:04:05Z", "shared history"))
	n, commit, err := ExportDeferred(dir, filepath.Join(tmp, "toLaptop"), "laptop")
	if err != nil || n != 1 {
		t.Fatalf("first peer export = %d, %v", n, err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	n2, _, err := ExportDeferred(dir, filepath.Join(tmp, "toServer"), "server")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		t.Fatalf("second peer export = %d, want 1 — a fresh peer must receive the history", n2)
	}
}

// Harnesses that stamp every message of a session with one timestamp (aider,
// Cursor) must not lose the messages appended after the first push.
func TestExportKeepsMessagesSharingTheWatermarkTimestamp(t *testing.T) {
	skipWindowsPortability(t)
	const ts = "2026-01-02T03:04:05Z"
	dir, tmp := syncPeerIndex(t, msgLine(ts, "first message"))
	n, commit, err := ExportDeferred(dir, filepath.Join(tmp, "out1"), "peer")
	if err != nil || n != 1 {
		t.Fatalf("first export = %d, %v", n, err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	body := msgLine(ts, "first message") + msgLine(ts, "second message same second")
	if err := os.WriteFile(filepath.Join(claudeRoot, "-tmp-app", "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(tmp, "out2")
	n2, _, err := ExportDeferred(dir, out2, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if n2 == 0 {
		t.Fatal("message appended with the same timestamp was never exported")
	}
	// And the receiver must end up with both messages.
	other := filepath.Join(tmp, "peer.db")
	if _, err := Import(other, filepath.Join(tmp, "out1")); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(other, out2); err != nil {
		t.Fatal(err)
	}
	got, err := Search(other, search.Options{Query: "second message same second", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("the appended message never reached the peer index")
	}
}

// Forgetting is primary data: a tombstoned session must not come back when
// the peer still holds the batch and this index was wiped and rebuilt.
func TestImportHonorsTombstonesAfterCacheWipe(t *testing.T) {
	dir, tmp := syncPeerIndex(t, msgLine("2026-01-02T03:04:05Z", "secret client work"))
	batch := filepath.Join(tmp, "batch")
	if _, err := Export(dir, batch); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(tmp, "peer.db")
	if _, err := Import(peer, batch); err != nil {
		t.Fatal(err)
	}
	imported := ImportedSessionID("claude", "s1")
	if _, err := Forget(peer, ForgetOptions{Session: imported}); err != nil {
		t.Fatal(err)
	}
	// The documented wipe procedure: delete the cache, keep the tombstones.
	if err := os.RemoveAll(peer); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(peer, batch); err != nil {
		t.Fatal(err)
	}
	got, err := Search(peer, search.Options{Query: "secret client work", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("forgotten session came back through sync: %d sessions", len(got))
	}
}

// The exclude list keeps a project out of this machine's memory; a sync from
// another machine must not put it back.
func TestImportHonorsExcludeList(t *testing.T) {
	skipWindowsPortability(t)
	dir, tmp := syncPeerIndex(t, msgLine("2026-01-02T03:04:05Z", "nda project notes"))
	batch := filepath.Join(tmp, "batch")
	if _, err := Export(dir, batch); err != nil {
		t.Fatal(err)
	}
	// Control: without an exclude list this batch does arrive. Without it a
	// typo in the pattern would make the assertion below pass for free.
	control := filepath.Join(tmp, "control.db")
	if _, err := Import(control, batch); err != nil {
		t.Fatal(err)
	}
	base, err := Search(control, search.Options{Query: "nda project notes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(base) == 0 {
		t.Fatal("fixture never imports at all — the exclude assertion would be vacuous")
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", exportedProject(t, batch))
	peer := filepath.Join(tmp, "peer.db")
	if _, err := Import(peer, batch); err != nil {
		t.Fatal(err)
	}
	got, err := Search(peer, search.Options{Query: "nda project notes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("excluded project arrived through sync: %d sessions", len(got))
	}
}

// exportedProject reads the project name straight off the wire, so the test
// excludes what actually travels rather than what the fixture path suggests.
func exportedProject(t *testing.T, batch string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(batch, "*.jsonl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no batch files in %s: %v", batch, err)
	}
	f, err := os.Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var rec SyncRecord
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		t.Fatal(err)
	}
	if rec.Project == "" {
		t.Fatal("exported record carries no project")
	}
	return rec.Project
}
