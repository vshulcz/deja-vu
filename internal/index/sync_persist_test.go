package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

func writeSyncBatch(t *testing.T, dir string, recs []SyncRecord) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "deja-sync-test-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Imported records exist only in the index, not in any source file. A full
// rebuild used to regenerate the index from sources alone and drop them, and
// redaction bookkeeping used to invent a phantom Files entry for the virtual
// deja-sync-import path that the next update classified as a removed source,
// purging every imported record.
func TestImportedRecordsSurviveRebuildAndUpdate(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-local")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"localneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "local1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "localneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	// Raw secret in the batch forces redaction bookkeeping during re-ingest,
	// which is what created the phantom Files entry.
	secret := "AKIA" + "IOSFODNN7EXAMPLE"
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	batchDir := filepath.Join(tmp, "batches")
	writeSyncBatch(t, batchDir, []SyncRecord{
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "user", Text: "importneedle question key " + secret, Time: base},
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "assistant", Text: "importneedle answer", Time: base.Add(time.Minute)},
	})
	if n, err := Import(dir, batchDir); err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	findImported := func(stage string) {
		t.Helper()
		ss, err := Search(dir, search.Options{Query: "importneedle", All: true})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") || len(ss[0].Messages) != 2 {
			t.Fatalf("%s: bad imported result: %#v", stage, ss)
		}
	}
	findImported("after import")

	// Full rebuild regenerates the index from sources; imported must survive.
	if err := EnsureForSearch(dir, search.Options{Query: "importneedle"}, true, nil); err != nil {
		t.Fatal(err)
	}
	findImported("after rebuild")

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[syncImportPath]; ok {
		t.Fatalf("phantom Files entry for %s", syncImportPath)
	}

	// Non-append update: one source removed, one added. This is the path that
	// used to purge imported records via the phantom removed entry.
	if err := os.Remove(filepath.Join(proj, "local1.jsonl")); err != nil {
		t.Fatal(err)
	}
	local2 := `{"type":"user","sessionId":"local2","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"othertext here"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "local2.jsonl"), []byte(local2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "importneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	findImported("after non-append update")

	// Dedupe map and watermarks must survive both paths.
	if n, err := Import(dir, batchDir); err != nil || n != 0 {
		t.Fatalf("re-import n=%d err=%v", n, err)
	}
	findImported("after re-import")

	// Export must not echo imported records back to their origin: only the
	// two native local messages leave this machine.
	outDir := filepath.Join(filepath.Dir(dir), "echo-out")
	n, err := Export(dir, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("export n=%d, want 1 native record only", n)
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "importneedle") {
			t.Fatalf("exported batch %s echoes imported records", p)
		}
	}

	// --full ignores watermarks (recovers history for a new machine) but
	// still never echoes imported records.
	fullDir := filepath.Join(filepath.Dir(dir), "echo-out-full")
	n, err = ExportFull(dir, fullDir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("full export n=%d, want 1 native record", n)
	}
	matches, err = filepath.Glob(filepath.Join(fullDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "importneedle") {
			t.Fatalf("full export batch %s echoes imported records", p)
		}
	}
}

// Two messages in one session can share a timestamp (aider); both must
// survive import, and re-import must stay a no-op.
func TestImportKeepsSameTimestampMessages(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		{Harness: "aider", SessionID: "same-ts", Project: "p", Role: "user", Text: "samets question", Time: base},
		{Harness: "aider", SessionID: "same-ts", Project: "p", Role: "assistant", Text: "samets answer", Time: base},
	})
	if n, err := Import(dir, batch); err != nil || n != 2 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	ss, err := Search(dir, search.Options{Query: "samets", All: true})
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("both messages must survive: %#v err=%v", ss, err)
	}
	if n, err := Import(dir, batch); err != nil || n != 0 {
		t.Fatalf("re-import n=%d err=%v", n, err)
	}
}

// The title does not travel in the record stream. Without deriving one, the
// receiving machine's `deja last` is a column of hashes even though the whole
// conversation arrived (#670).
func TestImportGivesSessionsATitle(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		// Out of order on purpose, and the first user turn by time is harness
		// plumbing: the title must be the earliest turn a person would
		// recognise, not the earliest record in the file.
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "assistant", Text: "capped at three", Time: base.Add(2 * time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "and what about jitter", Time: base.Add(3 * time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "the retry storm on checkout", Time: base.Add(time.Minute)},
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "<local-command-stdout>ok</local-command-stdout>", Time: base},
		// A session whose only user turns are plumbing falls back to the
		// assistant's first sentence rather than printing a blank line (#692).
		{Harness: "claude", SessionID: "s2", Project: "p", Role: "user", Text: "<command-name>/clear</command-name>", Time: base},
		{Harness: "claude", SessionID: "s2", Project: "p", Role: "assistant", Text: "cleared the branch", Time: base.Add(time.Minute)},
		// Even when the assistant spoke first, a later user turn still wins.
		{Harness: "claude", SessionID: "s3", Project: "p", Role: "assistant", Text: "capped the pool", Time: base},
		{Harness: "claude", SessionID: "s3", Project: "p", Role: "user", Text: "the pool starves", Time: base.Add(time.Minute)},
		// Two assistant turns: the earlier one is the title, or the last record
		// in the file decides instead of the clock.
		{Harness: "claude", SessionID: "s4", Project: "p", Role: "assistant", Text: "started the migration", Time: base},
		{Harness: "claude", SessionID: "s4", Project: "p", Role: "assistant", Text: "and then rolled it back", Time: base.Add(2 * time.Minute)},
	})
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, m := range metas {
		titles[m.ID] = m.Title
	}
	if len(titles) != 4 {
		t.Fatalf("expected four imported sessions, got %#v", titles)
	}
	seen := map[string]bool{}
	for _, ti := range titles {
		seen[ti] = true
	}
	for _, want := range []string{"the retry storm on checkout", "cleared the branch", "the pool starves", "started the migration"} {
		if !seen[want] {
			t.Errorf("title %q missing from %#v", want, titles)
		}
	}
}

// A wrong path imported nothing and exited 0 — including the case that matters,
// a typo for a directory that is right there with records in it (#678).
func TestImportRejectsAPathThatIsNotADirectoryOfRecords(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")

	missing := filepath.Join(tmp, "no-such-batch")
	_, err := Import(dir, missing)
	if err == nil {
		t.Error("importing a directory that does not exist succeeded")
		// The reader is looking for a typo, so the message has to name the
		// path they typed rather than repeat Go's stat syntax.
	} else if !strings.HasPrefix(err.Error(), "no such directory: ") || !strings.Contains(err.Error(), missing) {
		t.Errorf("error was %q", err)
	}
	plain := filepath.Join(tmp, "a-file")
	if err := os.WriteFile(plain, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, plain); err == nil {
		t.Error("importing a plain file succeeded")
	}
	// An empty directory that exists is a different answer: nothing to import
	// is not a mistake.
	empty := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, empty); err != nil || n != 0 {
		t.Errorf("empty batch: n=%d err=%v — an existing empty directory is not an error", n, err)
	}
}
