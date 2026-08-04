package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "`deja forget --list` names them" sent the reader to a list of everything the
// machine ever forgot, where the ones the refusal meant were theirs to find
// (#1014).
func TestUnforgetRefusalNamesTheSessionsItMeans(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess0", "sess1", "sess2", "alpha", "beta"} {
		rec := `{"type":"user","message":{"role":"user","content":"session ` + id + `"},"timestamp":"2026-08-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess0", "sess1", "sess2", "alpha", "beta"} {
		if _, err := captureRun(t, "forget", "--session", id); err != nil {
			t.Fatal(err)
		}
	}

	_, err := captureRun(t, "forget", "--unforget", "claude:sess")
	if err == nil {
		t.Fatal("an ambiguous selector was accepted")
	}
	for _, want := range []string{"claude:sess0", "claude:sess1", "claude:sess2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	// And not the ones it does not mean.
	if strings.Contains(err.Error(), "claude:alpha") {
		t.Errorf("the refusal named a session it would not restore: %v", err)
	}
	// Naming one still works.
	if _, err := captureRun(t, "forget", "--unforget", "claude:sess1"); err != nil {
		t.Errorf("naming one of them was refused: %v", err)
	}
}
