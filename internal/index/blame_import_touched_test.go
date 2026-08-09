package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A peer's file edits must be blamable after import. blame reads
// SessionMeta.Touched to find who changed a file; the import path built the
// imported meta without it, so `deja blame auth.go` showed only local edits
// even though the peer's `files` records were imported and searchable.
func TestImportPopulatesTouchedForBlame(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A foreign batch: a speech turn (so the session is real) and a files record
	// naming the edited path. Written by hand so it is unambiguously not this
	// machine's own export.
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lines := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"working on auth","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/auth.go","time":"` + when + `"}` + "\n"
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-peer.jsonl"), []byte(lines), 0o644); err != nil {
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
		if metas[i].OrigID == "peer1" || metas[i].ID != "" && metas[i].Project == "imported:svc" {
			found = &metas[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("imported session not found in %d metas", len(metas))
	}
	touchesAuth := false
	for _, p := range found.Touched {
		if p == "/repo/auth.go" {
			touchesAuth = true
		}
	}
	if !touchesAuth {
		t.Fatalf("imported session Touched=%v does not include /repo/auth.go — blame cannot attribute the peer's edit", found.Touched)
	}
}

// A reversal reported in an earlier import batch must survive a later batch
// that does not repeat it: import is incremental, so GaveUp unions with the
// row like Touched/Asked/Hit do, rather than being cleared by a quiet batch.
func TestImportGaveUpSurvivesALaterBatch(t *testing.T) {
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
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(shared, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gaveUpOf := func() bool {
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := range metas {
			if metas[i].OrigID == "peer2" || metas[i].Project == "imported:svc2" {
				return metas[i].GaveUp
			}
		}
		t.Fatal("imported session not found")
		return false
	}

	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	write("deja-sync-a.jsonl",
		`{"harness":"claude","session_id":"peer2","project":"svc2","role":"assistant","text":"reverted the retry cap, it made the queue worse","time":"`+t0+`"}`+"\n")
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	if !gaveUpOf() {
		t.Fatal("the reversal reported in the first batch was not marked")
	}

	// A later batch with more of the same session but no reversal line.
	t1 := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := os.Remove(filepath.Join(shared, "deja-sync-a.jsonl")); err != nil {
		t.Fatal(err)
	}
	write("deja-sync-b.jsonl",
		`{"harness":"claude","session_id":"peer2","project":"svc2","role":"assistant","text":"added a metric for the queue depth","time":"`+t1+`"}`+"\n")
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	if !gaveUpOf() {
		t.Error("a later quiet batch cleared the reversal mark from an earlier one")
	}
}
