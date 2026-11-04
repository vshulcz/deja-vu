package sources

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func writeCodexRollout(t *testing.T, dir string, compress bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "rollout-thread-7.jsonl")
	if err := os.WriteFile(plain, []byte(codexRolloutFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compress {
		return plain
	}
	out := plain + ".zst"
	if err := exec.CommandContext(t.Context(), "zstd", "-q", "-o", out, plain).Run(); err != nil {
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
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"), true)

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

// A compressed rollout was read whole and decompressed into a buffer before the
// first line was scanned, so one 4 GB rollout took more memory than the rest of
// the store. Almost all of it is records the parser drops; the same bytes read
// as plain .jsonl stayed at one line's worth.
func TestCodexStreamsACompressedRollout(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	if testing.Short() {
		t.Skip("compresses a 64 MB rollout")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("CODEX_HOME", root)
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	const size = 64 << 20
	ignored := `{"type":"turn_context","payload":{"context":"` + strings.Repeat("x", size/2) + `"}}` + "\n"
	cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
	cmd.Stdin = io.MultiReader(strings.NewReader(codexRolloutFixture), strings.NewReader(ignored), strings.NewReader(ignored))
	compressed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "rollout-thread-7.jsonl.zst")
	if err = os.WriteFile(path, compressed, 0o600); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ss, err := ParseCodexRollout(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "thread-7" || len(ss[0].Messages) != 2 ||
		!hasMessage(ss[0], "user", "why does the migration hang") ||
		!hasMessage(ss[0], "assistant", "the advisory lock was never released") {
		t.Fatalf("compressed messages: %+v", ss)
	}
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("reading a %d MB rollout allocated %d bytes", size>>20, alloc)
	// The streamed read allocated 16 MB locally; leave room for runtime noise.
	if alloc > 24<<20 {
		t.Errorf("reading a %d MB rollout allocated %d MB, want at most 24 MB", size>>20, alloc>>20)
	}
}

// A file that is not there, or that holds no frame to salvage, is an error rather
// than an empty rollout: the empty answer would be remembered as a read.
func TestZstdScanReportsAFileItCannotRead(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	dir := t.TempDir()
	drain := func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	}
	missing := filepath.Join(dir, "missing.jsonl.zst")
	if err := zstdScan(missing, "codex", drain); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	garbage := filepath.Join(dir, "garbage.jsonl.zst")
	if err := os.WriteFile(garbage, []byte("not a zstd frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := zstdScan(garbage, "codex", drain); err == nil {
		t.Error("a file with no frame in it read as an empty rollout")
	}
}

// The scan runs while zstd is still writing. A parse that stops early, with an
// error or a panic safeParse recovers further up, must not leave zstd blocked on
// a pipe nobody reads.
func TestZstdScanStopsOnErrorOrPanic(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	path := filepath.Join(root, "rollout.jsonl.zst")
	// More than a pipe holds: waiting without killing would block on stdout.
	if err := os.WriteFile(path, zstdFrame(t, strings.Repeat("x", 2<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("scan stopped")
	for _, panics := range []bool{false, true} {
		name := "error"
		if panics {
			name = "panic"
		}
		t.Run(name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != nil || panics {
					if err, ok := got.(error); !panics || !ok || !errors.Is(err, want) {
						t.Errorf("panic = %v, want %v", got, want)
					}
				}
			}()
			err := zstdScan(path, "codex", func(r io.Reader) error {
				var first [1]byte
				if _, err := io.ReadFull(r, first[:]); err != nil {
					t.Fatal(err)
				}
				if panics {
					panic(want)
				}
				return want
			})
			if !errors.Is(err, want) {
				t.Errorf("scan error = %v, want %v", err, want)
			}
		})
	}
}

func TestCodexReadsTheArchivedDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEX_ROOT", root)
	t.Setenv("DEJA_XCODE_CODEX_ROOT", filepath.Join(root, "absent"))
	writeCodexRollout(t, filepath.Join(root, "archived_sessions", "2026", "07", "17"), false)

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
	writeCodexRollout(t, dir, true)
	writeCodexRollout(t, dir, false)

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
	writeCodexRollout(t, filepath.Join(root, "sessions", "2026", "07", "17"), false)
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
