package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The tombstone is written before the rebuild that takes the records out. When
// the index cannot be written, forget handed back `mkdir /…/idx.tmp: permission
// denied` and left the reader believing the session was gone while search kept
// returning it (#976).
func TestForgetSaysWhatIsStillVisibleWhenTheIndexCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision about the retry counter"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s19","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s19.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "ro")
	dir := filepath.Join(parent, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755)
		_ = os.Chmod(dir, 0o755)
	})

	_, err := captureRun(t, "forget", "--session", "s19")
	if err == nil {
		t.Fatal("forget reported success with an index it could not write")
	}
	msg := err.Error()
	if strings.Contains(msg, ".tmp") || strings.Contains(msg, "mkdir") {
		t.Errorf("the failure names an internal path: %q", msg)
	}
	if !strings.Contains(msg, "check the directory's permissions") {
		t.Errorf("the failure does not say what to change: %q", msg)
	}
	if !strings.Contains(msg, "still in the index") {
		t.Errorf("the failure does not say the session is still visible: %q", msg)
	}
}

// forgetKeyOf answers "which session did this selector reach", which is what
// lets the failure above say whether the tombstone landed (#976).
func TestForgetKeyOfNamesTheSessionASelectorReached(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"abc123","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "abc123.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Session: "abc"}); got != "claude:abc123" {
		t.Errorf("forgetKeyOf(prefix) = %q, want claude:abc123", got)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Session: "nothing-like-it"}); got != "" {
		t.Errorf("a selector matching nothing named %q", got)
	}
	if got := forgetKeyOf(dir, index.ForgetOptions{Project: "proj"}); got != "" {
		t.Errorf("a project selector named a single session: %q", got)
	}
}

// The dry probe that decides the scope refusal fails the same ways the real
// pass does; handing its syscall back was the shape #798 replaced everywhere
// else (#976).
func TestForgetOnAVanishedIndexDiskNamesTheDisk(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"a session about the vault"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s22","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s22.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	// A mount point that is not there: the index directory and its parent are
	// both missing, which is what an ejected volume leaves behind.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "gone-volume", "index.db"))

	_, err := captureRun(t, "forget", "--session", "s22")
	if err == nil {
		t.Fatal("forget reported success against a vanished disk")
	}
	if !strings.Contains(err.Error(), "is not there") || !strings.Contains(err.Error(), "unmounted") {
		t.Errorf("the failure does not name the disk: %q", err.Error())
	}
	if strings.Contains(err.Error(), "mkdir") {
		t.Errorf("the failure is still a syscall: %q", err.Error())
	}
}
