package index

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// abortedPassIndex leaves an index behind whose last pass died between the
// parse and the manifest write, with exactly one unreadable line on disk.
func abortedPassIndex(t *testing.T) string {
	t.Helper()
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
	turn := func(ts, role, text string) string {
		return `{"type":"` + role + `","sessionId":"s1","cwd":"/tmp/app","timestamp":"` + ts +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	path := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(path, []byte(turn("2026-01-02T03:04:05Z", "user", "why does pgbouncer time out")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// One unreadable line and one good one: without the good one the pass has
	// no buckets to write and so never reaches the failure below.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(
		turn("2026-01-02T03:04:06Z", "user", "the pool \x1b[31mtimed out") +
			turn("2026-01-02T03:04:07Z", "assistant", "raised the pool to 40")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Kill the pass after the parse and before the manifest is written.
	buckets := filepath.Join(dir, "buckets")
	if err := os.Chmod(buckets, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err == nil {
		t.Fatal("the pass was supposed to fail before writing the manifest, so this measures nothing")
	}
	if err := os.Chmod(buckets, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func skipWhereADirectoryCannotBeMadeUnwritable(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop a write on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
}

// The malformed-line counters used to be cleared in one place: the manifest
// fold, inside writeManifest. A pass that died before it left its count behind
// for whoever parsed next, so one bad line on disk was reported as two — with
// the manifest agreeing, which is the worst shape for a number a person is
// meant to act on (#2010).
func TestAnAbortedPassDoesNotLendItsCountToTheNextOne(t *testing.T) {
	skipWhereADirectoryCannotBeMadeUnwritable(t)
	dir := abortedPassIndex(t)

	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()
	if !strings.Contains(said, "— 1 line skipped") {
		t.Errorf("one unreadable line on disk, and the run does not report exactly one:\n%s", said)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("one unreadable line on disk, manifest says %d", got)
	}
}

// The same leak by the path a recall takes. An aborted pass leaves records.bin
// short, so the next search finds the index damaged and rebuilds — through
// rebuildForSearch, which never passes through updateIndex, which is where the
// first fix for this put the drain.
func TestASearchRebuildDoesNotInheritADeadPassCount(t *testing.T) {
	skipWhereADirectoryCannotBeMadeUnwritable(t)
	dir := abortedPassIndex(t)

	if err := EnsureForSearch(dir, query.Options{}, false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.IngestHealth["claude"].MalformedLines; got != 1 {
		t.Errorf("one unreadable line on disk, the search rebuild recorded %d", got)
	}
}
