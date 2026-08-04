package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "pass a session id-prefix (see `deja last`)" is a step the reader can take
// and learn nothing from when the store is empty: the listing answers with the
// emptiness deja already knew about one command earlier (#992).
func TestIdNeededSaysTheStoreIsEmptyInsteadOfPointingAtTheListing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"promote", "share", "handoff"} {
		_, err := captureRun(t, cmd)
		if err == nil {
			t.Fatalf("%s accepted an empty store", cmd)
		}
		if strings.Contains(err.Error(), "deja last") {
			t.Errorf("%s sent the reader to a listing that is empty for the same reason: %v", cmd, err)
		}
		if !strings.Contains(err.Error(), "nothing is indexed yet") {
			t.Errorf("%s does not say the store is empty: %v", cmd, err)
		}
	}

	// With something indexed the refusal is about the missing argument again,
	// and the listing is worth reading.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "promote")
	if err == nil || !strings.Contains(err.Error(), "deja last") {
		t.Errorf("with sessions indexed promote stopped naming the listing: %v", err)
	}
}
