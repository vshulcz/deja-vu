package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The wording that separates a locked store from an empty machine lives in
// `emptyIndexHint`, and #1020 pinned the helper. What was not pinned is the two
// surfaces a person actually types: a query and `deja last` both answer from
// that helper, and a wiring change would let either call a machine empty while
// its sessions sit behind a wall doctor can see.
func TestAQueryAndLastNameAStoreTheyCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The sessions are readable first, so what follows is about the wall and
	// not about an empty machine.
	if out, err := captureRun(t, "pgbouncer"); err != nil || !strings.Contains(out, "s1") {
		t.Fatalf("the store did not answer before it was locked: %v %q", err, out)
	}

	proj := filepath.Join(claude, "-app")
	if err := os.Chmod(proj, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(proj, 0o755) })
	if err := os.RemoveAll(filepath.Join(tmp, "index.db")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"a query", []string{"pgbouncer"}},
		{"deja last", []string{"last", "3"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, _ := captureRunStderr(t, c.args...)
			if !strings.Contains(out, "could not be read") {
				t.Errorf("%s does not say a store was unreadable:\n%s", c.name, out)
			}
			if !strings.Contains(out, "deja doctor") {
				t.Errorf("%s does not say where to look:\n%s", c.name, out)
			}
			if strings.Contains(out, "no agent history was found") {
				t.Errorf("%s called the machine empty while its sessions sit behind a wall:\n%s", c.name, out)
			}
		})
	}
}
