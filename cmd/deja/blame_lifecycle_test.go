package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// blame answers "who decided this", and it answered with the accepted line of
// a decision that had been taken back: attachLifecycles ran on search hits
// only, and blame carries its own hit type (#1017).
func TestBlameCarriesTheDecisionThatHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"we raised the pool cap in pool.go to 200"},"timestamp":"2026-08-04T10:00:00Z","sessionId":"w25","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "w25", "--state", "accepted", "--note", "the pool cap is 200"); err != nil {
		t.Fatal(err)
	}

	// While the decision holds, blame says nothing extra.
	out, err := captureRun(t, "blame", "pool.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "tried and rejected") {
		t.Errorf("a decision that holds was reported as withdrawn:\n%s", out)
	}

	if _, err := captureRun(t, "promote", "w25", "--state", "rejected", "--note", "rolled back, the cap stays at 20"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "blame", "pool.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tried and rejected") {
		t.Errorf("blame reports a decision that was taken back as current:\n%s", out)
	}
	if !strings.Contains(out, "rolled back, the cap stays at 20") {
		t.Errorf("blame does not name the correction that won:\n%s", out)
	}
}
