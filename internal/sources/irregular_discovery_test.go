//go:build !windows

package sources

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}
}

// keepRegular is what the glob and Stat discovery paths lean on so a FIFO that
// matched their pattern never reaches a parser's Open.
func TestKeepRegularDropsNonRegular(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg.jsonl")
	if err := os.WriteFile(reg, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.jsonl")
	mkfifoOrSkip(t, fifo)

	got := keepRegular([]string{reg, fifo, filepath.Join(dir, "missing.jsonl")})
	if len(got) != 1 || got[0] != reg {
		t.Fatalf("keepRegular = %v, want just %q", got, reg)
	}
}

// The antigravity glob and the aider Stat scan both once returned a named pipe
// that would block indexing. Their discovery must drop it.
func TestAntigravityAndAiderSkipNamedPipes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	logs := filepath.Join(root, "brain", "traj", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	mkfifoOrSkip(t, filepath.Join(logs, "transcript.jsonl"))
	if got := AntigravityTranscripts(); len(got) != 0 {
		t.Errorf("antigravity returned a named pipe: %v", got)
	}

	aroot := t.TempDir()
	t.Setenv("DEJA_AIDER_ROOTS", aroot)
	mkfifoOrSkip(t, filepath.Join(aroot, ".aider.chat.history.md"))
	for _, f := range AiderFiles() {
		if f == filepath.Join(aroot, ".aider.chat.history.md") {
			t.Errorf("aider returned a named pipe: %v", f)
		}
	}
}
