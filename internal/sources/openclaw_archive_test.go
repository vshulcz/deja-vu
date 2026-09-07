package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func openclawArchiveBody(id, said string) string {
	return `{"type":"session","id":"` + id + `","cwd":"/work/app","timestamp":"2026-08-02T10:00:00Z"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"` + said + `"}]},"timestamp":"2026-08-02T10:01:00Z"}` + "\n"
}

// A reset or a delete renames the transcript rather than removing it, and that
// file is exactly the history someone comes to deja for after losing it — the
// old rule matched sessions/*.jsonl and let it fall out of the index (#2997).
func TestOpenClawReadsWhatAResetLeftBehind(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "aaaa1111.jsonl.reset.1788000000")
	if err := os.WriteFile(archive, []byte(openclawArchiveBody("aaaa1111", "the reset queue keeps double-acking")), 0o644); err != nil {
		t.Fatal(err)
	}

	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != archive {
		t.Fatalf("the archived transcript is not offered to the index: %#v", files)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("archive parsed to %d sessions: %v", len(ss), err)
	}
	// The id it had before the rename: that is what the session key mapping and
	// anyone looking for it still use.
	if ss[0].ID != "aaaa1111" {
		t.Fatalf("session id = %q, want the id from before the rename", ss[0].ID)
	}
	if ss[0].Path != archive {
		t.Fatalf("session path = %q, want the archive it was read from", ss[0].Path)
	}
	if len(ss[0].Messages) == 0 || !strings.Contains(ss[0].Messages[0].Text, "double-acking") {
		t.Fatalf("the archived turn did not come through: %#v", ss[0].Messages)
	}
}

// Not every transcript carries a session header, and the file name is the only
// place the id is then: the parser strips ".jsonl", which on an archive leaves
// the rename stamp in the id someone would look it up by.
func TestOpenClawArchiveKeepsTheIDFromBeforeTheRename(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "eeee5555.jsonl.deleted.1788000002")
	headerless := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"no header on this one"}]},"timestamp":"2026-08-02T10:01:00Z"}` + "\n"
	if err := os.WriteFile(archive, []byte(headerless), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("archive parsed to %d sessions: %v", len(ss), err)
	}
	if ss[0].ID != "eeee5555" {
		t.Fatalf("session id = %q, want the id from before the rename", ss[0].ID)
	}
}

// A reset starts a new transcript under the old name. Reading both would index
// one conversation twice, so the live file wins.
func TestOpenClawSkipsAnArchiveWhoseLiveFileIsBack(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	live := filepath.Join(sessions, "bbbb2222.jsonl")
	if err := os.WriteFile(live, []byte(openclawArchiveBody("bbbb2222", "the live turn")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live+".reset.1788000000", []byte(openclawArchiveBody("bbbb2222", "the archived turn")), 0o644); err != nil {
		t.Fatal(err)
	}
	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != live {
		t.Fatalf("want only the live transcript, got %#v", files)
	}
}

// Since the SQLite flip an explicit delete writes the archive compressed. zstd
// is already what the Zed and DeepSeek stores are read through.
func TestOpenClawReadsACompressedDeletedTranscript(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is not installed; the compressed archive path needs it")
	}
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	plain := filepath.Join(sessions, "cccc3333.jsonl.deleted.1788000001")
	if err := os.WriteFile(plain, []byte(openclawArchiveBody("cccc3333", "we settled on one shard")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}
	archive := plain + ".zst"
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
	files := OpenClawSessionFiles()
	if len(files) != 1 || files[0] != archive {
		t.Fatalf("the compressed archive is not offered to the index: %#v", files)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil || len(ss) != 1 {
		t.Fatalf("compressed archive parsed to %d sessions: %v", len(ss), err)
	}
	if ss[0].ID != "cccc3333" || !strings.Contains(ss[0].Messages[0].Text, "one shard") {
		t.Fatalf("the deleted session did not come back: %#v", ss[0])
	}
	// The archive, not the temporary copy it was decompressed into: the path is
	// what `deja show` prints and what the next index pass stats.
	if ss[0].Path != archive {
		t.Fatalf("session path = %q, want the archive itself", ss[0].Path)
	}
}

// A compaction checkpoint is a context snapshot rather than a conversation, and
// it stays out — the rule that lets archives in must not let those in with them.
func TestOpenClawStillSkipsCheckpoints(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	name := "dddd4444.checkpoint.aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.jsonl"
	if err := os.WriteFile(filepath.Join(sessions, name), []byte(openclawArchiveBody("dddd4444", "snapshot")), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := OpenClawSessionFiles(); len(files) != 0 {
		t.Fatalf("a checkpoint was offered to the index: %#v", files)
	}
}
