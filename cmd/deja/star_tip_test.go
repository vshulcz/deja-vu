package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func TestStarTipLine(t *testing.T) {
	dir := t.TempDir()

	// Under 15 recalls returns "" and does not write the stamp.
	if got := starTipLine(dir, 0); got != "" {
		t.Fatalf("starTipLine(0) = %q, want empty", got)
	}
	if got := starTipLine(dir, 14); got != "" {
		t.Fatalf("starTipLine(14) = %q, want empty", got)
	}
	if got := starTipLine("", 15); got != "" {
		t.Fatalf("starTipLine(empty dir) = %q, want empty", got)
	}
	stamp := filepath.Join(dir, "star-tip-shown")
	if _, err := os.Stat(stamp); err == nil {
		t.Fatalf("stamp file %s exists prematurely", stamp)
	}

	// At >= 15 recalls returns star tip on first call and writes stamp.
	const want = "  If it helps      gh repo star vshulcz/deja-vu · https://github.com/vshulcz/deja-vu"
	if got := starTipLine(dir, 15); got != want {
		t.Fatalf("starTipLine(15) = %q, want %q", got, want)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("stamp file %s missing after first call: %v", stamp, err)
	}

	// Second call returns empty.
	if got := starTipLine(dir, 15); got != "" {
		t.Fatalf("starTipLine(15) second call = %q, want empty", got)
	}
	if got := starTipLine(dir, 50); got != "" {
		t.Fatalf("starTipLine(50) subsequent call = %q, want empty", got)
	}
}

func TestStarTipSharedGroupMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX group permissions are not modeled on Windows")
	}
	t.Setenv("DEJA_SHARED_GROUP", "1")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if got := starTipLine(dir, 25); got == "" {
		t.Fatal("expected star tip line")
	}
	if n := index.UnsharedFiles(dir); n != 0 {
		t.Fatalf("index.UnsharedFiles(%s) = %d after starTipLine, want 0", dir, n)
	}
}

func TestDoctorSharedIndexClientSuppressesDeniedExternalStoreWarning(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission refusal requires non-root POSIX permissions")
	}
	tmp := hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "proj", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00Z","cwd":"/work/app","message":{"role":"user","content":"shared index session"}}`,
	})
	if err := index.Ensure(dir, "", true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	lockedDir := filepath.Join(tmp, "root-only", "opencode")
	if err := os.MkdirAll(lockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(lockedDir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootOnly := filepath.Join(tmp, "root-only")
	if err := os.Chmod(rootOnly, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rootOnly, 0o755) })

	t.Setenv("DEJA_OPENCODE_DB", dbPath)
	t.Setenv("DEJA_SHARED_GROUP", "1")
	t.Setenv("DEJA_NO_AUTO_INDEX", "1")

	var out bytes.Buffer
	printDoctorText(&out, collectDoctorReport(func() (string, bool) { return "", false }, dir), nil, dir, false, true)
	text := out.String()
	if strings.Contains(text, "opencode     unplugged") {
		t.Errorf("shared-index client reported EACCES store as unplugged:\n%s", text)
	}
	if strings.Contains(text, "warning      opencode") {
		t.Errorf("shared-index client warned on external indexer's store:\n%s", text)
	}
	if !strings.Contains(text, "opencode     shared") || !strings.Contains(text, "read by the shared indexer") {
		t.Errorf("expected opencode row to say shared (read by the shared indexer):\n%s", text)
	}
}
