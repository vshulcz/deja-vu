package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// The boundary set is what lets a second push skip records that share the
// watermark instant. Every rebuild of the manifest must carry it, or ordinary
// ingest between two pushes silently turns it back into a resend.
func TestBoundarySurvivesIncrementalIngest(t *testing.T) {
	skipWindowsPortability(t)
	const ts = "2026-01-02T03:04:05Z"
	dir, tmp := syncPeerIndex(t, msgLine(ts, "only message"))
	n, commit, err := ExportDeferred(dir, filepath.Join(tmp, "out1"), "laptop")
	if err != nil || n != 1 {
		t.Fatalf("first export = %d, %v", n, err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	// Ordinary incremental ingest: an unrelated project appears. syncSSHPush
	// runs EnsureForSearch before every export, so this is the common path.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	other := filepath.Join(claudeRoot, "-tmp-other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "s2.jsonl"),
		[]byte(msgLine("2026-01-03T00:00:00Z", "unrelated work")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ExportBoundary) == 0 {
		t.Fatal("ingest dropped the export boundary; the next push resends the watermark instant forever")
	}
	// And the behavior that matters: the already-delivered record stays home.
	n2, _, err := ExportDeferred(dir, filepath.Join(tmp, "out2"), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 1 {
		t.Fatalf("second export = %d records, want 1 (only the new session)", n2)
	}
}

// Watermarks are per source. A push that touches project B must not disturb
// what project A has already delivered.
func TestUnrelatedPushDoesNotWipeOtherBoundaries(t *testing.T) {
	skipWindowsPortability(t)
	const ts = "2026-01-02T03:04:05Z"
	dir, tmp := syncPeerIndex(t, msgLine(ts, "project a message"))
	if _, commit, err := ExportDeferred(dir, filepath.Join(tmp, "a1"), "laptop"); err != nil {
		t.Fatal(err)
	} else if err := commit(); err != nil {
		t.Fatal(err)
	}
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.ExportBoundary) != 1 {
		t.Fatalf("boundary entries after first push = %d, want 1", len(before.ExportBoundary))
	}
	// A second project shows up and gets pushed.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	other := filepath.Join(claudeRoot, "-tmp-other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "s2.jsonl"),
		[]byte(msgLine("2026-01-03T00:00:00Z", "project b message")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, commit, err := ExportDeferred(dir, filepath.Join(tmp, "b1"), "laptop"); err != nil {
		t.Fatal(err)
	} else if err := commit(); err != nil {
		t.Fatal(err)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.ExportBoundary) != 2 {
		t.Fatalf("boundary entries = %d, want 2 — a push for one source wiped another's", len(after.ExportBoundary))
	}
}

// Excluding a project must also drop what a peer already pushed, not only
// what arrives next: the exclude list is a privacy control, not a filter on
// new traffic.
func TestExcludeAppliesToAlreadyImportedSessions(t *testing.T) {
	dir, tmp := syncPeerIndex(t, msgLine("2026-01-02T03:04:05Z", "nda project notes"))
	batch := filepath.Join(tmp, "batch")
	if _, err := Export(dir, batch); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(tmp, "peer.db")
	if _, err := Import(peer, batch); err != nil {
		t.Fatal(err)
	}
	got, err := Search(peer, search.Options{Query: "nda project notes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("fixture never imported — the assertion below would be vacuous")
	}
	// The user excludes the project afterwards and the index is rebuilt.
	t.Setenv("DEJA_EXCLUDE_PROJECTS", exportedProject(t, batch))
	if err := Ensure(peer, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err = Search(peer, search.Options{Query: "nda project notes", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("excluded project survived the rebuild: %d sessions", len(got))
	}
}

// Glob swallows a directory it cannot open, so a locked source imported
// "0 records" — the words an already-imported batch prints, while the records
// were there the whole time (#1042).
func TestImportSaysWhenTheSourceCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	src := t.TempDir()
	line := `{"harness":"claude","session_id":"peer3","project":"work/api","role":"user","text":"a third peer decision","time":"2026-08-04T10:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(src, "deja-sync-x.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o755) })

	n, err := Import(dir, src)
	if err == nil {
		t.Fatalf("a locked source imported %d records without a word", n)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}

	// A readable directory with nothing in it is a different answer and stays
	// a quiet zero.
	if n, err := Import(dir, t.TempDir()); err != nil || n != 0 {
		t.Errorf("an empty source returned %d, %v; want 0 and no error", n, err)
	}
}
