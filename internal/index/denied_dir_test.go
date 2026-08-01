package index

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A directory the process cannot read takes its sessions out of recall, and
// nothing downstream could attribute it: the walk dropped EACCES, and a
// diagnostic path with no transcript extension matched no harness (#818).
func TestUnreadableDirectoryIsAttributedToItsHarness(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	root, dir := allHarnessEnv(t)
	write(t, filepath.Join(root, "claude", "-tmp-open", "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "manneedle here"))
	locked := filepath.Join(root, "claude", "-tmp-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(locked, "s.jsonl"),
		claudeLine("s2", "2026-01-03T03:04:05Z", "manneedle in the locked half"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if err := EnsureForSearch(dir, search.Options{Query: "manneedle", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	health := IngestHealth(dir)
	e, ok := health["claude"]
	if !ok {
		t.Fatalf("no ingest health for claude: %v", health)
	}
	if e.FailedFiles == 0 {
		t.Errorf("the unreadable directory was not counted: %+v", e)
	}
	if e.LastError == "" {
		t.Errorf("no error text recorded: %+v", e)
	}
}

// The mapping a directory needs: transcript matchers reject it, so what a
// transcript inside it would be is the question that answers.
func TestHarnessForPathHandlesDirectories(t *testing.T) {
	root, _ := allHarnessEnv(t)
	claudeDir := filepath.Join(root, "claude", "-tmp-p")
	if got := harnessForPath(claudeDir); got == "" {
		t.Errorf("a directory under the claude root mapped to no harness")
	}
	if got := harnessForPath(filepath.Join(claudeDir, "s.jsonl")); got == "" {
		t.Errorf("a transcript under the claude root mapped to no harness")
	}
	if got := harnessForPath(filepath.Join(t.TempDir(), "elsewhere")); got != "" {
		t.Errorf("an unrelated directory mapped to %q", got)
	}
}
