package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An imported session lives only in the index — forget rewrote the log and the
// rebuild has nothing to re-read, so the undo lifted the tombstone and claimed
// a restore that did not happen (#967).
func TestUnforgetSaysWhenAnImportedSessionCannotComeBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "peer/api", Role: "user", Text: "peer text"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	var imported string
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "imported-") {
			imported = m.ID
		}
	}
	if imported == "" {
		t.Fatal("no imported session in the index")
	}

	if _, err := captureRunStderr(t, "forget", "--session", imported); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "forget", "--unforget", "claude:"+imported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "restored 1 session") {
		t.Errorf("the undo claimed a restore that did not happen: %q", out)
	}
	if !strings.Contains(out, "sync import") {
		t.Errorf("the undo does not name the way back: %q", out)
	}

	// A local session still round-trips, and still says so.
	if _, err := captureRunStderr(t, "forget", "--session", "loc"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "forget", "--unforget", "claude:loc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored 1 session") {
		t.Errorf("a local session no longer reports its restore: %q", out)
	}
}

// The undo names `sync import` as the way back; this checks the way back
// actually works. Forgetting an imported session dropped it from the manifest
// but left its rows in the import dedupe ledger, so the re-import the message
// told the user to run was silently deduped away and the "only copy" was
// unrecoverable. Import now skips a ledger row only while its session still
// lives, so an unforgotten session comes back.
func TestUnforgetImportedComesBackOnReimport(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	// Three imported sessions so a forget is unambiguous and the count moves.
	var batch []byte
	for i := 1; i <= 3; i++ {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "pe" + string(rune('0'+i)), Project: "svc",
			Role: "user", Text: "rotate the signing key, take " + string(rune('0'+i)),
			Time: time.Date(2026, time.Month(i), 1, 10, 0, 5, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	sessions := func() int {
		ov, err := index.Overview(dir)
		if err != nil {
			t.Fatal(err)
		}
		return ov.Sessions
	}
	if sessions() != 3 {
		t.Fatalf("import: want 3 sessions, got %d", sessions())
	}

	var id string
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.ID, "imported-") {
			id = m.ID
			break
		}
	}
	if _, err := captureRunStderr(t, "forget", "--session", id); err != nil {
		t.Fatal(err)
	}
	if sessions() != 2 {
		t.Fatalf("forget: want 2 sessions, got %d", sessions())
	}
	// While forgotten the tombstone must hold: re-import does not resurrect it.
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	if sessions() != 2 {
		t.Fatalf("re-import while forgotten resurrected it: want 2, got %d", sessions())
	}
	// Undo the tombstone, then re-import: now it comes back.
	if _, err := captureRun(t, "forget", "--unforget", "claude:"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	if got := sessions(); got != 3 {
		t.Fatalf("re-import after unforget did not bring the session back: want 3, got %d", got)
	}
}
