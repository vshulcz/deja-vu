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
// `emptyIndexHint`. #1020 pinned the helper, and friction is pinned through a
// command — but a query and `deja last` each return through
// `hiddenByOwnSettings` before reaching the helper, a branch friction does not
// have, and nothing held either of them to it.
func TestAQueryAndLastNameAStoreTheyCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	// hermeticEnv rather than four Setenvs: XDG_CONFIG_HOME leaking from the
	// machine is enough to fail both cases, because a `deja/exclude` file there
	// sends the answer through hiddenByOwnSettings before the hint is reached.
	tmp := hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")

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
