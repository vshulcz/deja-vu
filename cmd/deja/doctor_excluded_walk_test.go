package main

import (
	"os"
	"path/filepath"
	"testing"
)

// doctor's store checks listed every store's files before asking whether the
// store was excluded, and `deja index` runs them, so an excluded store was
// walked on every index pass (#5394).
func TestDoctorDoesNotListAnExcludedStore(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)
	roll := filepath.Join(root, "sessions", "2026", "07", "17", "rollout-2026-07-17T09-00-00-0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001.jsonl")
	if err := os.MkdirAll(filepath.Dir(roll), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(roll, []byte(`{"timestamp":"2026-07-17T09:00:00.000Z","type":"session_meta","payload":{"id":"0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001","cwd":"/tmp/proj"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codexFiles := func() (int, bool) {
		for _, c := range doctorStoreChecks() {
			if c.name == "codex" {
				return len(c.files), true
			}
		}
		return 0, false
	}
	if n, _ := codexFiles(); n != 1 {
		t.Fatalf("control: codex lists %d files with nothing excluded, want 1", n)
	}
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")
	n, row := codexFiles()
	if !row {
		t.Fatal("the excluded store lost its row, which is where doctor says `excluded`")
	}
	if n != 0 {
		t.Errorf("doctor listed %d files of an excluded store", n)
	}
}
