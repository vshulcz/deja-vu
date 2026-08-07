package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A built index whose directory cannot be read (permissions, a restricted
// mount) was reported as "not built (run `deja warmup`)" — advice that fails
// the same way and hides a build sitting behind a closed door (#1116).
func TestDoctorNamesAnUnreadableIndex(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file makes it a real, non-empty index directory.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	var out bytes.Buffer
	doctorIndex(&out, doctorComponent{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "unreadable") || !strings.Contains(got, "permission denied") {
		t.Errorf("a blocked index was not named unreadable:\n%s", got)
	}
	if strings.Contains(got, "not built (run `deja warmup`)") {
		t.Errorf("a blocked index was called not-built:\n%s", got)
	}
}
