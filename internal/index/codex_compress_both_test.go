package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A pass that lands between zstd writing x.jsonl.zst and removing x.jsonl
// sees both forms. Once the .jsonl goes, the session must hold its messages
// once, as a rebuild of what is left does (#4252).
func TestACodexRolloutSeenInBothFormsIsHeldOnceAfterTheJSONLGoes(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	p := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	write(t, p, codexRolloutHead)
	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "-k", p).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v %s", err, out)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	count := func(idx string) int {
		recs, err := ReadRecords(idx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if r.Record.Key == "codex:cx-1" && strings.Contains(r.Record.Text, "go test") {
				n++
			}
		}
		return n
	}
	fresh := filepath.Join(tmp, "fresh")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := count(idx), count(fresh); got != want {
		t.Errorf("the command is held %d times after the update, %d after a rebuild", got, want)
	}
}
