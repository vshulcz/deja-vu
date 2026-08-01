package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Silence reads as "it did not run". `update` on the newest release and
// `doctor` on a fresh index both say so; this one returned to the prompt with
// nothing (#824).
func TestIndexSaysWhenThereIsNothingToDo(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"rudder trunk seal"},"timestamp":"2026-07-04T10:00:00Z","sessionId":"u9","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "up to date") {
		t.Errorf("the first build claimed there was nothing to do:\n%s", first)
	}

	second, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "up to date") {
		t.Errorf("a no-op run said nothing:\n%s", second)
	}

	// The warmup child runs this command; returning before the sentinel is
	// cleared leaves a build that is not running (#839).
	sentinel := filepath.Join(t.TempDir(), "warmup.sentinel")
	if err := os.WriteFile(sentinel, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_WARMUP_SENTINEL", sentinel)
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("a run with nothing to do left its warmup sentinel behind")
	}

	// --rebuild is explicit work, never "nothing to do".
	forced, err := captureRunStderr(t, "index", "--rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(forced, "up to date") {
		t.Errorf("--rebuild reported nothing to do:\n%s", forced)
	}

	// And the line belongs to `index` alone: a search that finds the index
	// fresh must not narrate it.
	found, err := captureRunStderr(t, "--all", "rudder")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(found, "up to date") {
		t.Errorf("a search narrated the index state:\n%s", found)
	}

	if fresh, n := index.UpToDate(indexDirForTest(), ""); !fresh || n == 0 {
		t.Errorf("UpToDate = %v, %d", fresh, n)
	}
}
