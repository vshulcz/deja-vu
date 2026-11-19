package index

import (
	search "github.com/vshulcz/deja-vu/internal/query"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// What a version difference costs the first question after an upgrade. A store
// this build can read answers under its own older rules while the re-read runs
// behind it; one whose layout this build cannot read, or whose text predates a
// redaction fix, does not answer until it has been rebuilt (#3552, #3535).
func TestOnlyALayoutOrARedactionBumpStopsAnIndexFromAnswering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     Manifest
		build int
		must  bool
	}{
		{
			name:  "the index this build writes",
			m:     Manifest{Version: version, Format: onDiskFormat},
			build: version,
		},
		{
			// The case this exists for: the next derivation bump, reading the
			// index the build before it wrote.
			name:  "a derivation bump this build can read",
			m:     Manifest{Version: redactionFloor, Format: onDiskFormat},
			build: redactionFloor + 1,
		},
		{
			name:  "text written before deja could redact it",
			m:     Manifest{Version: redactionFloor - 1, Format: onDiskFormat},
			build: version,
			must:  true,
		},
		{
			name:  "a layout this build cannot read",
			m:     Manifest{Version: version, Format: onDiskFormat - 1},
			build: version,
			must:  true,
		},
		{
			// Left alone and answered from: rebuilding it down had two installs
			// rebuild the store back and forth.
			name:  "an index from a newer build in the same layout",
			m:     Manifest{Version: version + 1, Format: onDiskFormat},
			build: version,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRebuildBeforeAnswering(tc.m, tc.build); got != tc.must {
				t.Fatalf("mustRebuildBeforeAnswering = %v, want %v", got, tc.must)
			}
		})
	}
}

// The floor is a claim about the version list in index.go, not a free number:
// it names the last bump that changed what may be shown, so it can never point
// past the version this build writes.
func TestTheRedactionFloorNamesAVersionThatExists(t *testing.T) {
	if redactionFloor > version {
		t.Fatalf("redactionFloor %d is past version %d — every index would rebuild before answering", redactionFloor, version)
	}
	if redactionFloor <= 0 {
		t.Fatalf("redactionFloor = %d", redactionFloor)
	}
}

// An index written before the redaction bump answers nothing until it has been
// rebuilt: its text predates the patterns that mask a password given to a
// program as a flag, and the latency-bound path must not serve it while the
// re-read runs (#3535, #3552).
func TestAnIndexBelowTheRedactionFloorRebuildsBeforeItAnswers(t *testing.T) {
	dir := indexWithOneSession(t)
	agedManifest(t, dir, redactionFloor-1)

	stale, err := EnsureForSearchStale(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("served an index whose text predates the redaction it needs")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("index left at version %d — it was supposed to be rebuilt before answering", m.Version)
	}
}

// A layout this build cannot read is the other case that blocks, and it is the
// one onDiskFormat exists to mark.
func TestAnUnreadableLayoutRebuildsBeforeItAnswers(t *testing.T) {
	dir := indexWithOneSession(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Format = onDiskFormat - 1
	writeAgedManifest(t, dir, m)

	stale, err := EnsureForSearchStale(dir, search.Options{All: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("served a store written in a layout this build cannot read")
	}
	if m, err := readManifest(dir); err != nil || m.Format != onDiskFormat {
		t.Fatalf("format=%d err=%v — the store was supposed to be rebuilt", m.Format, err)
	}
}

func indexWithOneSession(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the retry budget was the thing that fixed it"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func agedManifest(t *testing.T, dir string, v int) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = v
	writeAgedManifest(t, dir, m)
}

// writeAgedManifest writes the manifest and moves its mtime, because the
// manifest cache keys on mtime and size and a version field is the same size
// either way.
func writeAgedManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	tick := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), tick, tick); err != nil {
		t.Fatal(err)
	}
}

// Seven parser fixes changed what the readers index (#3289); a store built
// at the previous version keeps the old rows until it is rebuilt, and the
// rebuild only happens when the version says so. A store at 34 with its
// sessions file present is exactly what an upgrade finds on disk.
func TestAStoreFromBeforeTheParserFixesIsNotCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifest(dir, Manifest{Version: 34}); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("a version-34 store reads as current, so nothing re-reads the sources after the parser fixes")
	}
}

// Four changes in a row widened what counts as a wall — the quote rule, the
// generic prefix, nine phrases, the line cap — and each declined a version
// bump. The signatures live in the manifest, and nothing re-derives them: a
// store built under the old rules keeps answering by them, so `hook-tool-after`
// stays silent and the environment block stays empty on the machine that has
// been running deja longest (#2444).
func TestTheVersionMovesWhenTheRulesDo(t *testing.T) {
	// 30 was the last bump (masking digit runs, #2369). Everything since has
	// changed which lines become signatures at all.
	if version <= 30 {
		t.Errorf("version is %d: the friction rules moved and the version did not, "+
			"so every index built before them answers by the old rules forever", version)
	}
}

// Version 12 reshards non-ASCII tokens, so an index left at an older version
// must be rebuilt before it is searched — and every script must survive that
// rebuild. (This covers the upgrade path, not the shape of a genuine v11
// store: several tiers enumerate bucket directories directly, so a v11 store
// stays partly reachable rather than going dark.)
func TestOlderIndexVersionIsRebuiltBeforeSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"обсуждали миграцию базы и 数据库迁移 plus asciimarker"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Pretend this index was written by the previous version.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = 11
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// The manifest cache keys on mtime+size, and a version field is the same
	// size either way — move the mtime so the cache cannot serve the old copy.
	tick := time.Now().Add(2 * time.Second)
	if err = os.Chtimes(filepath.Join(dir, "manifest.gob"), tick, tick); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(dir, search.Options{Query: "миграцию", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != version {
		t.Fatalf("index still at version %d — a stale-version index was served, not rebuilt", got.Version)
	}
	for _, q := range []string{"миграцию", "数据库迁移", "asciimarker"} {
		ss, err := Search(dir, search.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) == 0 {
			t.Errorf("query %q found nothing after the version upgrade", q)
		}
	}
}

// Hook paths never call Ensure, so they must be able to tell an older store
// apart and ask for a rebuild instead of reading it. Reading a v11 store with
// v12 code returns nothing for Russian and CJK while English still works —
// silent, and the user has no way to see why.
func TestIsCurrentVersionDetectsOlderStore(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"обсуждали миграцию базы"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if !IsCurrentVersion(dir) {
		t.Fatal("a freshly built index must report as current")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = version - 1
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("an older store reported as current — hook paths would read it and silently recall nothing")
	}
	// And a store that was never built is not "current" either.
	if IsCurrentVersion(filepath.Join(tmp, "nope")) {
		t.Fatal("a missing index reported as current")
	}
}
