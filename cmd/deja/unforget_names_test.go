package main

import (
	"fmt"
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

// The restore is the moment someone checks they got back exactly what they
// lost, and it answered with a count while the ids were already in hand
// (#1095). The ids must be the ones actually restored, not every session the
// selector matches after the rebuild.
func TestTheRestoreNamesWhatCameBack(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("sess%d", i)
		rec := `{"type":"user","message":{"role":"user","content":"decision ` + id + `"},"timestamp":"2026-07-1` + fmt.Sprint(i) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude:sess1", "claude:sess3"} {
		if _, err := captureRun(t, "forget", "--session", id); err != nil {
			t.Fatal(err)
		}
	}

	out, err := captureRun(t, "forget", "--unforget", "claude:sess", "--all-matches")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude:sess1") || !strings.Contains(out, "claude:sess3") {
		t.Errorf("the restore does not name what came back:\n%s", out)
	}
	// sess0 and sess2 were never forgotten; naming them would be a lie about
	// what this call did.
	for _, untouched := range []string{"claude:sess0", "claude:sess2"} {
		if strings.Contains(out, untouched) {
			t.Errorf("the restore names %s, which it did not restore:\n%s", untouched, out)
		}
	}
}
