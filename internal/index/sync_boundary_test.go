package index

import (
	"os"
	"path/filepath"
	"testing"

	search "github.com/vshulcz/deja-vu/internal/query"
)

// The boundary set is what lets a second push skip records that share the
// watermark instant. Every rebuild of the manifest must carry it, or ordinary
// ingest between two pushes silently turns it back into a resend.
func TestBoundarySurvivesIncrementalIngest(t *testing.T) {
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
