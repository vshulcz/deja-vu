package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// noTempDir points every temporary-directory variable at a file, so anything
// that writes a temporary file fails instead of leaving one.
func noTempDir(t *testing.T) {
	t.Helper()
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, blocked)
	}
}

// A compressed transcript was decompressed into $TMPDIR before parsing: the
// unredacted plaintext, left there by a pass killed mid-parse (#5052). Both
// readers now parse it from memory and need no temporary directory at all.
func TestCompressedTranscriptsNeedNoTempFile(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	rollout := writeCodexRollout(t, filepath.Join(t.TempDir(), "sessions"), true)

	sessions := filepath.Join(t.TempDir(), "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(sessions, "cccc3333.jsonl.deleted.1788000001")
	if err := os.WriteFile(plain, []byte(openclawArchiveBody("cccc3333", "we settled on one shard")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}

	noTempDir(t)
	ss, err := ParseCodexRolloutFromOffset(rollout, 0)
	if err != nil || len(ss) != 1 || ss[0].ID != "thread-7" || len(ss[0].Messages) != 2 || ss[0].Path != rollout {
		t.Fatalf("codex rollout: %+v, %v", ss, err)
	}
	ss, err = ParseOpenClawFile(plain + ".zst")
	if err != nil || len(ss) != 1 || ss[0].ID != "cccc3333" || ss[0].Path != plain+".zst" {
		t.Fatalf("openclaw archive: %+v, %v", ss, err)
	}
}
