package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func seedTwoTranscripts(t *testing.T) (dir, claudeRoot, s1, s2 string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot = filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 = filepath.Join(proj, "s1.jsonl")
	s2 = filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the zorblax pool deadlocked"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-08-02T03:04:05Z","message":{"role":"user","content":"the quuxbar worker stalled"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	// Tombstones resolve through XDG_CONFIG_HOME, which TestMain sets once for
	// the whole package: a forget here would otherwise reach every later test
	// that happens to name a session s1, and did.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir = filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir, claudeRoot, s1, s2
}

func sessionsFor(t *testing.T, dir, q string) []string {
	t.Helper()
	ss, err := Search(dir, search.Options{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// The incremental pass keeps a session after the client deletes its transcript
// (#2970). A full rebuild reads the sources and writes what it read, so it
// wrote the store without them: 6 of 8 sessions gone on a measured store, and
// a rebuild is what a content-version bump, a changed exclude list and a
// damaged index all run (#3529).
func TestAFullRebuildKeepsADeletedTranscript(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: the incremental pass kept it.
	if got := sessionsFor(t, dir, "zorblax"); len(got) != 1 {
		t.Fatalf("the incremental pass did not keep the deleted transcript: %v", got)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", true, &log); err != nil {
		t.Fatal(err)
	}
	if got := sessionsFor(t, dir, "zorblax"); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("the rebuild dropped the session whose transcript was deleted: %v\nlog: %s", got, log.String())
	}
	if got := sessionsFor(t, dir, "quuxbar"); len(got) != 1 {
		t.Errorf("the rebuild lost the session still on disk: %v", got)
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the rebuild carried a deleted transcript and said nothing:\n%s", log.String())
	}
	// The manifest keeps its row, so the next pass does not see it as removed.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[s1]; !ok {
		t.Errorf("the carried transcript has no file row: %v", sortedKeys(m.Files))
	}
	// And a second rebuild neither drops it nor writes it twice.
	before, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() {
		t.Errorf("a second rebuild changed records.bin from %d to %d bytes", before.Size(), after.Size())
	}
	if got := sessionsFor(t, dir, "zorblax"); len(got) != 1 {
		t.Errorf("the second rebuild left %d copies of the carried session: %v", len(got), got)
	}
}

// A store that is gone whole is an uninstall or a disk that is not mounted,
// and the rebuild drops it as it always did — the carry is for a file deleted
// out of a store that is still there.
func TestAFullRebuildDropsAStoreThatWentAway(t *testing.T) {
	dir, claudeRoot, _, _ := seedTwoTranscripts(t)
	if err := os.RemoveAll(claudeRoot); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionsFor(t, dir, "zorblax"); len(got) != 0 {
		t.Errorf("a store that went away was carried anyway: %v", got)
	}
}

// `deja forget` is the deliberate path, and the carry must not undo it: the
// records are gone, and the file row it leaves behind must not come back as a
// transcript deja claims is still searchable.
func TestAFullRebuildDoesNotResurrectAForgottenSession(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "claude:s1"}); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionsFor(t, dir, "zorblax"); len(got) != 0 {
		t.Errorf("the rebuild brought a forgotten session back: %v", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[s1]; ok {
		t.Errorf("the forgotten transcript kept its file row, so deja counts a transcript it no longer holds")
	}
	if got := sessionsFor(t, dir, "quuxbar"); len(got) != 1 {
		t.Errorf("forgetting one session cost the other: %v", got)
	}
}

// A loss that cannot be avoided is still said out loud. An upgrade across a
// record-layout change cannot decode what the old store wrote, and a transcript
// that is gone from disk has nowhere else to come from: measured against the
// released binaries, a store from 0.19.5 upgrades with those sessions
// unrecoverable. Silence there reads as "nothing was lost" (#3529).
func TestARebuildSaysWhichKeptSessionsItCannotCarry(t *testing.T) {
	dir, _, path, _ := seedTwoTranscripts(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// The records this build would carry, in a shape it cannot read: an empty
	// file stands in for a layout it no longer decodes, since the manifest
	// still lists the row and the walk comes back with nothing.
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", true, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "cannot read those records") {
		t.Errorf("the rebuild dropped a kept session and said nothing about it:\n%s", log.String())
	}
	// And the ordinary case still says the other sentence, not this one.
	dir2, _, path2, _ := seedTwoTranscripts(t)
	if err := os.Remove(path2); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir2, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := Ensure(dir2, "claude", true, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "cannot read those records") {
		t.Errorf("a rebuild that carried everything claimed it could not:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "still searchable") {
		t.Errorf("the rebuild carried a session and said nothing:\n%s", log.String())
	}
}
