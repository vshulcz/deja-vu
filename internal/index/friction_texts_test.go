package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The walls' texts come from frictext.bin without a pass over the log, and a
// file that does not fit the log, whatever is wrong with it, gives what
// reading the sessions back gives.
func TestFrictionTextsAnswerOrFallBack(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	walls := []string{"make: *** [widget] Error 2", "zsh:1: command not found: frobnicate"}
	for w, wall := range walls {
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("w%ds%d", w, i)
			at := fmt.Sprintf("2026-07-01T1%d:%02d:00Z", w, i*10)
			body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":"the build keeps stopping"}}` + "\n" +
				`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + wall + `"}]}}` + "\n"
			p := filepath.Join(root, "-w-app", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, frictionTextsFile)
	good, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("a build leaves no %s: %v", frictionTextsFile, err)
	}
	top := func() (string, int64) {
		invalidateManifestCache(dir)
		before := RecordLogScans()
		got := TopFriction(dir, 0, nil)
		return fmt.Sprint(got), RecordLogScans() - before
	}
	want, scans := top()
	if scans != 0 {
		t.Fatalf("with %s TopFriction took %d passes over the log", frictionTextsFile, scans)
	}
	otherLog := append([]byte(nil), good...)
	otherLog[16] ^= 0xff // inside the log fingerprint
	broken := map[string][]byte{
		"missing":   nil,
		"truncated": good[:len(good)-3],
		"bad magic": append([]byte("XXXXXXXX"), good[8:]...),
		"other log": otherLog,
	}
	for name, b := range broken {
		if b == nil {
			_ = os.Remove(file)
		} else if err := os.WriteFile(file, b, 0o600); err != nil {
			t.Fatal(err)
		}
		got, scans := top()
		if got != want {
			t.Errorf("%s file: %s, want %s", name, got, want)
		}
		if scans == 0 {
			t.Errorf("%s file: answered without reading the sessions", name)
		}
	}
}
