package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An import writes the manifest, and until now it wrote the build time with
// it. Anything asking "is a store behind?" compares the newest file in that
// store against the build time, so on a machine that syncs on a timer — this
// one imports from a peer under launchd — every store read as freshly indexed
// while local transcripts sat unread. That is the surface half of #3747: ten
// transcripts the index had never read, and doctor saying nothing.
func TestAnImportDoesNotClaimTheStoresWereRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	session := `{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the pool hands out dead connections after a deploy"}}
{"type":"assistant","sessionId":"aaa1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the proxy closes idle backends at five minutes"}}
`
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	read := ManifestSourcesReadAt(dir)
	if read.IsZero() {
		t.Fatal("a build that walked the stores recorded no time for it")
	}
	builtBefore := ManifestBuiltAt(dir)

	// A peer's records, imported the way `deja sync` imports them.
	in := filepath.Join(tmp, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := SyncRecord{
		Harness: "claude", SessionID: "peer1", Project: "pool", Role: "user",
		Text: "the peer machine hit the same dead connection after its deploy", Time: time.Now(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "peer.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if n, err := Import(dir, in); err != nil || n == 0 {
		t.Fatalf("import wrote %d records: %v", n, err)
	}

	built := ManifestBuiltAt(dir)
	after := ManifestSourcesReadAt(dir)
	if !after.Equal(read) {
		t.Errorf("the import moved the sources-read time from %s to %s; it read no local transcript",
			read.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	}
	// And this is the mechanism, stated as an assertion: the import does move
	// the build time, which is what made every store look freshly read.
	if !built.After(builtBefore) {
		t.Errorf("the import left the build time at %s; the whole point is that it moves it",
			built.Format(time.RFC3339Nano))
	}

	// And a pass that does walk the stores moves it.
	if err := os.WriteFile(filepath.Join(root, "bbb2.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if again := ManifestSourcesReadAt(dir); !again.After(read) {
		t.Errorf("a pass that read the stores left the read time at %s", again.Format(time.RFC3339Nano))
	}
}

// An older store has no read time in it, and the answer for those is the build
// time rather than the zero value — which would report every store behind.
func TestAStoreWithoutTheFieldFallsBackToTheBuildTime(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude", "projects", "-work-pool")
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"),
		[]byte(`{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"an older store, written before the field existed"}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.SourcesReadAt = time.Time{}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got, want := ManifestSourcesReadAt(dir), ManifestBuiltAt(dir); !got.Equal(want) {
		t.Errorf("a store with no read time answered %s; the build time is %s",
			got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// The bootstrap shape: an import on a machine that has never indexed writes a
// manifest with no files at all, so that the next pass ingests everything
// (#1307). Nothing local has been read there, and answering with the import's
// own clock is what made doctor call a store current when it had not been
// opened.
func TestAnIndexBuiltOnlyFromImportsHasReadNothing(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		Version:  version,
		Format:   onDiskFormat,
		Files:    map[string]FileState{},
		Sessions: map[string]SessionMeta{},
		BuiltAt:  time.Now(),
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := ManifestSourcesReadAt(dir); !got.IsZero() {
		t.Errorf("an import-built index says it read the stores at %s", got.Format(time.RFC3339Nano))
	}
	if built := ManifestBuiltAt(dir); built.IsZero() {
		t.Error("the same index has no build time, so the two are not being told apart")
	}
}
