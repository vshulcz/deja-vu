package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Codex compresses a rollout once it is seven days old — `COMPRESSED_SUFFIX`
// and `MIN_ROLLOUT_AGE` in its own rollout/compression.rs, run by a background
// worker — and reads either name itself. deja wanted `.jsonl` alone, so on a
// machine where that worker had run, every session older than a week left the
// index without a word: not a candidate, therefore not a skip either (#3640).
//
// `archived_sessions` is the same store's second directory, Codex's own
// `ARCHIVED_SESSIONS_SUBDIR`, and a session moved into it used to disappear on
// the next pass.
const codexRolloutFixture = `{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"thread-7","cwd":"/work/api"}}
{"timestamp":"2026-07-17T09:00:01.000Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"why does the migration hang"}]}}
{"timestamp":"2026-07-17T09:00:02.000Z","type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"the advisory lock was never released"}]}}
`

func writeCodexRollout(t *testing.T, dir, name string, compress bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, name)
	if err := os.WriteFile(plain, []byte(codexRolloutFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compress {
		return plain
	}
	out := plain + ".zst"
	if err := exec.Command("zstd", "-q", "-o", out, plain).Run(); err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCodexReadsACompressedRollout(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"), "rollout-thread-7.jsonl", true)

	files := CodexFiles()
	if len(files) != 1 || !strings.HasSuffix(files[0], ".jsonl.zst") {
		t.Fatalf("a compressed rollout is not a candidate: %v", files)
	}

	ss := LoadCodex()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1: %+v", len(ss), ss)
	}
	if ss[0].ID != "thread-7" {
		t.Errorf("id = %q, want the thread id from the meta line", ss[0].ID)
	}
	if len(ss[0].Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(ss[0].Messages))
	}
	if !strings.Contains(ss[0].Messages[1].Text, "advisory lock") {
		t.Errorf("the reply did not survive decompression: %q", ss[0].Messages[1].Text)
	}
	// The session keeps the name of the file on disk, not of the temporary copy
	// it was read through: `deja doctor` reports a transcript no longer present
	// by comparing this against the store.
	if !strings.HasSuffix(ss[0].Path, ".jsonl.zst") {
		t.Errorf("path = %q, want the compressed file", ss[0].Path)
	}
}

func TestCodexReadsTheArchivedDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "archived_sessions", "2026", "07", "17"),
		"rollout-thread-7.jsonl", false)

	ss := LoadCodex()
	if len(ss) != 1 || ss[0].ID != "thread-7" {
		t.Fatalf("an archived session is not read: %+v", ss)
	}
	// It is a transcript, not unaccounted content: doctor's sidecar row must
	// not claim it.
	for _, f := range CodexSidecarFiles() {
		if strings.Contains(f, "archived_sessions") {
			t.Errorf("an archived rollout is counted as a sidecar: %s", f)
		}
	}
}

// Codex materializes a compressed rollout back to plain before appending, so
// both names exist for one session while that happens. Reading both would
// double the session; the plain one wins because it is the one being written.
func TestCodexReadsOneFilePerSession(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	dir := filepath.Join(root, "sessions", "2026", "07", "17")
	writeCodexRollout(t, dir, "rollout-thread-7.jsonl", true)
	writeCodexRollout(t, dir, "rollout-thread-7.jsonl", false)

	if n := len(CodexFiles()); n != 2 {
		t.Fatalf("both files should be visible to diagnostics, got %d", n)
	}
	ss := LoadCodex()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 — the same session under two names", len(ss))
	}
	if strings.HasSuffix(ss[0].Path, ".zst") {
		t.Errorf("the compressed copy won: %s", ss[0].Path)
	}
}

// Without zstd the store says so, the way DeepSeek Harness and Zed already do.
// A store of plain rollouts must not claim a problem it does not have.
func TestCodexSaysWhenItNeedsZstd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"),
		"rollout-thread-7.jsonl", false)
	if got := SkipReason("codex"); got != "" {
		t.Errorf("a plain store explains nothing, got %q", got)
	}

	// A compressed rollout beside it: the answer depends on the tool, and on a
	// machine that has it there is still nothing to explain.
	if err := os.WriteFile(filepath.Join(root, "sessions", "2026", "07", "17",
		"rollout-thread-8.jsonl.zst"), []byte("not a real frame"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := SkipReason("codex")
	if ZstdAvailable() {
		if got != "" {
			t.Errorf("zstd is installed, so nothing is missing, got %q", got)
		}
		return
	}
	if got != "zstd CLI not found" {
		t.Errorf("SkipReason = %q, want the missing tool named", got)
	}
}
