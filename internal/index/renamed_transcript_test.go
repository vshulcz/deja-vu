package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func seedOneTranscript(t *testing.T, name, text string) (dir, path string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-shulcz-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(proj, name)
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
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
	return dir, path
}

func recordsSize(t *testing.T, dir string) int64 {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// A transcript under a new name was read from the first byte and written again:
// twenty renames of one 4 KB log left twenty-one copies in records.bin and
// twenty rows pointing at paths that were gone, with search answering once and
// no screen saying the store was twenty times its content (#3546).
func TestARenamedTranscriptIsNotIndexedAgain(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	before := recordsSize(t, dir)
	renamed := filepath.Join(filepath.Dir(path), "s1-v3.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if after := recordsSize(t, dir); after != before {
		t.Errorf("records.bin went from %d to %d bytes for a file that only changed its name", before, after)
	}
	if !strings.Contains(log.String(), "renamed") {
		t.Errorf("the pass said nothing about the rename:\n%s", log.String())
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("the old name was kept as a transcript deja still holds:\n%s", log.String())
	}
	ss, err := Search(dir, search.Options{Query: "zorblax"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("after the rename the session answers %d times, want 1", len(ss))
	}
	if ss[0].Path != renamed {
		t.Errorf("the session still points at %q, want %q", ss[0].Path, renamed)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[path]; ok {
		t.Errorf("the old path kept a file row: %v", sortedKeys(m.Files))
	}
	if _, ok := m.Files[renamed]; !ok {
		t.Errorf("the new path has no file row: %v", sortedKeys(m.Files))
	}
	// And the pass after it has nothing to do.
	log.Reset()
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if after := recordsSize(t, dir); after != before {
		t.Errorf("the following pass rewrote records.bin: %d against %d", after, before)
	}
}

// The pairing is on content. A file of the same size holding a different
// conversation is a new transcript, and the one that went away is kept, not
// renamed away.
func TestADifferentFileIsNotTakenForARename(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Same length, different text and a different session id.
	other := filepath.Join(filepath.Dir(path), "s2.jsonl")
	line := `{"type":"user","sessionId":"s2","timestamp":"2026-06-02T03:04:05Z","message":{"role":"user","content":"the quuxbar pool deadlocked"}}` + "\n"
	if err := os.WriteFile(other, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "renamed") {
		t.Errorf("a different conversation was taken for a rename:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the deleted transcript stopped being searchable: %d hits", len(ss))
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxbar"}); len(ss) != 1 {
		t.Errorf("the new transcript was not indexed: %d hits", len(ss))
	}
}

// The conditions themselves, without the filesystem: a row with no fingerprint
// is left alone, and two harnesses are never paired.
func TestRenameDetectionIsNarrow(t *testing.T) {
	old := map[string]FileState{
		"/gone/a.jsonl":       {Path: "/gone/a.jsonl", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/gone/nohash.jsonl":  {Path: "/gone/nohash.jsonl", Size: 200, SafeSize: 200},
		"/gone/other.jsonl":   {Path: "/gone/other.jsonl", Size: 300, SafeSize: 300, PrefixSample: 9},
		"/still/here.jsonl":   {Path: "/still/here.jsonl", Size: 400, SafeSize: 400, PrefixSample: 11},
		"deja-sync-import":    {Path: "deja-sync-import", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/gone/sizeonly.json": {Path: "/gone/sizeonly.json", Size: 500, SafeSize: 500, PrefixSample: 13},
	}
	files := map[string]FileState{
		"/new/a.jsonl":      {Path: "/new/a.jsonl", Size: 100, SafeSize: 100, PrefixSample: 7},
		"/new/nohash.jsonl": {Path: "/new/nohash.jsonl", Size: 200, SafeSize: 200},
		"/new/other.jsonl":  {Path: "/new/other.jsonl", Size: 300, SafeSize: 300, PrefixSample: 8},
		"/still/here.jsonl": {Path: "/still/here.jsonl", Size: 400, SafeSize: 400, PrefixSample: 11},
	}
	got := detectRenamedFiles(old, files)
	if len(got) != 1 {
		t.Fatalf("paired %d files, want 1: %v", len(got), got)
	}
	if got["/new/a.jsonl"] != "/gone/a.jsonl" {
		t.Errorf("pairing = %v, want /new/a.jsonl from /gone/a.jsonl", got)
	}
}

// A resumed session arrives under a new name with a turn appended. The
// fingerprint covers the bytes deja had already read, so the pairing still
// holds and only the tail is parsed.
func TestARenamedTranscriptThatGrewReadsOnlyItsTail(t *testing.T) {
	dir, path := seedOneTranscript(t, "s1.jsonl", "the zorblax pool deadlocked")
	before := recordsSize(t, dir)
	renamed := filepath.Join(filepath.Dir(path), "s1-resumed.jsonl")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(renamed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-06-02T03:05:05Z","message":{"role":"assistant","content":"quuxbar was the fix"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	var log bytes.Buffer
	if err := Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "renamed") {
		t.Errorf("a renamed file that grew was not recognised:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "zorblax"}); len(ss) != 1 {
		t.Errorf("the first turn answers %d times, want 1", len(ss))
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxbar"}); len(ss) != 1 {
		t.Errorf("the appended turn answers %d times, want 1", len(ss))
	}
	// The tail, not the whole file: the growth is one short turn.
	if grew := recordsSize(t, dir) - before; grew > before {
		t.Errorf("records.bin grew by %d bytes for a one-turn append to a %d byte store", grew, before)
	}
}
