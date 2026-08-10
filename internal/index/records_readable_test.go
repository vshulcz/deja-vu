package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// An incremental append grows records.bin before it rewrites the manifest, so a
// concurrent lock-free reader sees the new size against the old stamp. That must
// not read as corruption (a needless full rebuild / a hard error), because the
// extra bytes are an uncommitted tail no bucket references.
func TestSearchToleratesAnInFlightAppend(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "jwt refresh rotation reconciler"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate the append window: records.bin is longer than the manifest's
	// committed size (an uncommitted tail).
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("uncommitted appended tail bytes not in any bucket")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// The read path must still answer, not fail as corrupt.
	res, err := SearchDetailed(dir, query.Options{Query: "reconciler"})
	if err != nil {
		t.Fatalf("a longer records.bin surfaced as an error: %v", err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the committed session was not found through the in-flight window")
	}

	// A SHORTER records.bin is still real truncation.
	if err := os.Truncate(rp, 10); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if recordsReadable(dir, m) {
		t.Error("a truncated records.bin was accepted as readable")
	}
}

// The keyless/regex query full-scans records.bin to EOF, so it actually reads
// the uncommitted tail. A garbage tail (a valid session's record appended but a
// second, torn or unknown-session record after it) must not corrupt the answer:
// the committed session is still found, and the tail contributes nothing.
func TestRegexScanToleratesAnInFlightTail(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "REGEXMARK committed content"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Append a plausible-looking but uncommitted tail (a short length header +
	// bytes), the shape an in-flight append leaves before the manifest updates.
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// A 4-byte little-endian length header claiming a small record, then a
	// truncated payload — decodes to a short read, treated as a clean EOF.
	if _, err := f.Write([]byte{20, 0, 0, 0, 'u', 'n', 'c', 'o', 'm', 'm'}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// A regex query takes the full-scan path (no postings keys).
	res, err := SearchDetailed(dir, query.Options{Query: "REGEXMARK", Regex: true})
	if err != nil {
		t.Fatalf("the full-scan path errored on an in-flight tail: %v", err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the committed session was lost on the full-scan path")
	}
}
