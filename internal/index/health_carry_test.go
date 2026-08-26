package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The health map is how doctor answers "what could deja not read" — and the
// answer has to outlive an unrelated pass, because the lines it counts are
// still on disk. The merge path built a fresh manifest and carried five fields
// forward but not this one, so the whole map went at the next pass that took it
// (#2015).
func TestTheHealthMapSurvivesAnUnrelatedPass(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := func(id, ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + id + `","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	// The second line carries a raw escape, which makes it invalid JSON.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
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
	// The premise: the build that read the bad line recorded it. Without this
	// the assertions below would hold on a store that never lost anything.
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Fatalf("the build recorded %d unreadable lines, so there is nothing to carry", got)
	}

	// A new session in the same store — a change that does not touch the file
	// holding the bad line. This is the merge path.
	if err := os.WriteFile(filepath.Join(proj, "s2.jsonl"), []byte(
		turn("s2", "2026-02-02T03:04:05Z", "user", "unrelated question about retries")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 2 {
		t.Fatalf("the pass indexed %d sessions, so it was not the merge path this is about", len(m.Sessions))
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("the unreadable line is still on disk and the count is %d: doctor has forgotten it", got)
	}

	// The other half: a file rewritten without its bad line clears its own
	// count, or carrying counts forward would make them permanent.
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool timed out")+
			turn("s1", "2026-01-02T03:04:07Z", "assistant", "raised it to 40")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 0 {
		t.Errorf("the bad line is gone from the file and the count is still %d", got)
	}
}

// healthFixture is one claude store and the paths to build it with.
func healthFixture(t *testing.T) (dir, proj string, turn func(id, ts, role, text string) string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj = filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	turn = func(id, ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + id + `","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	return filepath.Join(tmp, "index.db"), proj, turn
}

func malformedFor(t *testing.T, dir, harness string) int {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m.IngestHealth[harness].MalformedLines
}

// The append path reads the tail, not the file, so what it finds adds to the
// file's count. Treating the file as re-read dropped every bad line already
// indexed — which is every live session, since a session that continues is
// exactly what this path is for.
func TestAppendingToASessionKeepsItsEarlierCount(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build recorded %d, so the appends below measure nothing", got)
	}

	grow := func(ts, text string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(turn("s1", ts, "assistant", text)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	grow("2026-01-02T03:05:00Z", "raised the pool to 40")
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Errorf("the session continued and the count went to %d, but the bad line is still in the file", got)
	}
	grow("2026-01-02T03:06:00Z", "and it \x1b[31mstill timed out")
	if got := malformedFor(t, dir, "claude"); got != 2 {
		t.Errorf("two bad lines in the file, count says %d", got)
	}
}

// A manifest written without a parse — Import writes one, and `deja sync` runs
// it in the same process right after an index pass — must not take the pass's
// files with it.
func TestAManifestWrittenWithoutAParseKeepsTheCounts(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")+
			turn("s1", "2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build recorded %d, so this measures nothing", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := malformedFor(t, dir, "claude"); got != 1 {
		t.Errorf("a manifest write that parsed nothing left the count at %d", got)
	}
}

// The clip count is this pass's — it is recorded during redaction, before the
// fold — so seeding the new manifest with the old total made every re-read of a
// clipped file add to it: one long message counted as two, then three.
func TestAClippedMessageIsCountedOncePerPass(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	if err := os.WriteFile(path, []byte(turn("s1", "2026-01-02T03:04:05Z", "user", long)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	clipped := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.IngestHealth["claude"].ClippedMessages
	}
	if got := clipped(); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}
	for i, tail := range []string{"first answer", "second answer", "third answer"} {
		if err := os.WriteFile(path, []byte(
			turn("s1", "2026-01-02T03:04:05Z", "user", long)+
				turn("s1", "2026-01-02T03:04:06Z", "assistant", tail)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if got := clipped(); got != 1 {
			t.Fatalf("one long message in the file, rewrite %d says %d clipped", i+1, got)
		}
	}
}

// A file that failed to open once and then reads fine has to stop being
// reported: transcripts are append-only, so the append path is the one that
// reads it again, and it is the path that cannot clear an entry by re-reading.
// Left alone, a permission blip stayed in doctor until a forced rebuild.
func TestAFileThatOpensAgainStopsBeingReportedUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not stop a read here")
	}
	dir, proj, turn := healthFixture(t)
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(turn("s1", "2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	failed := func() int {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.IngestHealth["claude"].FailedFiles
	}
	if got := failed(); got != 1 {
		t.Fatalf("the unreadable file was reported %d times, so this measures nothing", got)
	}

	// The blip passes and the session carries on, which is an append.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(turn("s1", "2026-01-02T03:05:00Z", "assistant", "raised the pool to 40")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := failed(); got != 0 {
		t.Errorf("deja read the file and still reports %d unreadable", got)
	}
}

func clippedFor(t *testing.T, dir, harness string) int {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m.IngestHealth[harness].ClippedMessages
}

// Clips were the last ingest number kept per harness, so a pass over some other
// file reset them to what it found — nothing — while the long message sat where
// it always had (#2022).
func TestAClipSurvivesAPassOverAnotherFile(t *testing.T) {
	dir, proj, turn := healthFixture(t)
	long := strings.Repeat("pgbouncer pool timed out and the retry took a second ", 1600)
	if len(long) < maxIndexedText {
		t.Fatalf("the fixture message is %d bytes, under the %d that gets it clipped", len(long), maxIndexedText)
	}
	a := filepath.Join(proj, "a.jsonl")
	b := filepath.Join(proj, "b.jsonl")
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", long)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(turn("b", "2026-01-02T03:04:05Z", "user", "a short question")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Fatalf("the build clipped %d messages, so this measures nothing", got)
	}

	// b rewritten shorter. A rewrite that only grows is still an append, and
	// the append path keeps the old manifest — this is the path that does not.
	if err := os.WriteFile(b, []byte(turn("b", "2026-01-02T03:04:05Z", "user", "short")), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, "incremental index") {
		t.Fatalf("this was not the merge path, so it does not measure what it is about: %q", said)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Errorf("the long message is still in the other file and the count is %d", got)
	}

	// And the file that holds it, re-read by the merge path: counted once, not
	// once more. Shorter than it was so the pass cannot append, still long
	// enough to be clipped.
	shorter := long[:len(long)-1000]
	if len(shorter) < maxIndexedText {
		t.Fatalf("the re-read fixture is %d bytes, under the %d that gets it clipped", len(shorter), maxIndexedText)
	}
	if err := os.WriteFile(a, []byte(turn("a", "2026-01-02T03:04:05Z", "user", shorter)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if said := out.String(); !strings.Contains(said, "incremental index") {
		t.Fatalf("the re-read did not take the merge path: %q", said)
	}
	if got := clippedFor(t, dir, "claude"); got != 1 {
		t.Errorf("one long message, count says %d", got)
	}
}
