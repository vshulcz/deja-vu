package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Codex compresses a finished rollout in place, x.jsonl to x.jsonl.zst. When
// nothing else in the pass shrank, the vanished .jsonl was kept as a cleanup
// and the pass took the append path, which has no rename rule: the .zst's
// records went in beside the old ones and every message showed twice, under a
// path that no longer exists (#4252).
func TestACodexRolloutCompressedInPlaceIsAMove(t *testing.T) {
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
	if out, err := exec.CommandContext(t.Context(), "zstd", "-q", "--rm", p).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v %s", err, out)
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if row := m.Sessions["codex:cx-1"]; row.Path != p+".zst" || row.Shared {
		t.Errorf("row on %s shared=%v, want it moved to the .zst", row.Path, row.Shared)
	}
	if _, ok := m.Files[p]; ok {
		t.Errorf("the old path %s is still held", p)
	}
	recs, err := ReadRecords(idx)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, r := range recs {
		if r.Record.Key == "codex:cx-1" && strings.Contains(r.Record.Text, "go test") {
			cmds = append(cmds, r.Record.SourcePath)
		}
	}
	if len(cmds) != 1 {
		t.Errorf("the command is held %d times, from %q; want once", len(cmds), cmds)
	}
}
