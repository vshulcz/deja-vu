package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The walk's budget counted every entry, so on a store of tens of thousands of
// transcripts it was spent inside the first project and a locked directory
// later in the walk was never reached (#864).
func TestDeniedDirIsFoundPastTheFileBudget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root := t.TempDir()
	crowd := filepath.Join(root, "aaa-crowded")
	if err := os.MkdirAll(crowd, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6000; i++ {
		if err := os.WriteFile(filepath.Join(crowd, fmt.Sprintf("s%d.jsonl", i)), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(root, "zzz-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got := firstDeniedDir([]string{root}); got != locked {
		t.Errorf("denied dir = %q, want %q", got, locked)
	}
}
