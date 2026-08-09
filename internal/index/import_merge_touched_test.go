package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A peer's session grows and is re-exported: the second sync batch carries only
// the new records (the old ones are already in the import ledger and skipped).
// The imported meta was rebuilt from just that batch and overwrote Touched, so a
// file the peer edited in the first batch stopped being blamable after the
// second import. The merge must keep the earlier files, not replace them.
func TestReimportKeepsEarlierTouchedFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}

	t1 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	batch1 := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"working on auth","time":"` + t1 + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/auth.go","time":"` + t1 + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-a.jsonl"), []byte(batch1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}

	// Second batch: the same session, later, touching a different file.
	t2 := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	batch2 := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"now the database layer","time":"` + t2 + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/db.go","time":"` + t2 + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-b.jsonl"), []byte(batch2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found *SessionMeta
	for i := range metas {
		if metas[i].OrigID == "peer1" {
			found = &metas[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("imported session not found in %d metas", len(metas))
	}
	has := func(p string) bool {
		for _, x := range found.Touched {
			if x == p {
				return true
			}
		}
		return false
	}
	if !has("/repo/db.go") {
		t.Errorf("Touched=%v missing the new file /repo/db.go", found.Touched)
	}
	if !has("/repo/auth.go") {
		t.Errorf("Touched=%v dropped /repo/auth.go from the first import — blame can no longer attribute it", found.Touched)
	}
}
