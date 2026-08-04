package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A peer who still holds the batch hands it over again, which is the ordinary
// case — the same folder synced twice. The record is dropped for two reasons at
// once, and only one of them is worth saying: the dedupe ledger answered first,
// so the import went silent exactly when it had something to report, while a
// wiped-and-rebuilt index said it (#980).
func TestReimportOfAForgottenSessionStillSaysItWasLeftOut(t *testing.T) {
	tmp := t.TempDir()
	// Tombstones live under XDG_CONFIG_HOME, which the package shares: one
	// left behind here reads as forgotten in every test that runs after.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "solo", Project: "work/api", Role: "user", Text: "peer record about the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, exp)
	if err != nil || n != 1 {
		t.Fatalf("first import: %d records, %v", n, err)
	}
	if ImportSkippedForgotten() != 0 {
		t.Fatalf("a fresh batch was reported as forgotten: %d", ImportSkippedForgotten())
	}

	id := ImportedSessionID("claude", "solo")
	if _, err := Forget(dir, ForgetOptions{Session: "claude:" + id}); err != nil {
		t.Fatal(err)
	}

	n, err = Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a forgotten session came back: %d records", n)
	}
	if got := ImportSkippedForgotten(); got != 1 {
		t.Errorf("the re-import dropped the record silently: skipped=%d", got)
	}
}
