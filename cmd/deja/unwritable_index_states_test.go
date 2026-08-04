package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// One state, three stories: search worded it, stats handed back the syscall,
// and doctor called it ordinary staleness and advised the one command that
// cannot succeed here (#1004).
func TestAnUnwritableIndexReadsTheSameEverywhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// A store that changed after the build, and no way to write the result.
	newer := `{"type":"user","message":{"role":"user","content":"a session added after the parent was locked"},"timestamp":"2026-08-04T12:00:00Z","sessionId":"new","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "new.jsonl"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err := captureRun(t, "stats")
	if err == nil {
		t.Fatal("stats did not fail on an index it cannot write")
	}
	if strings.Contains(err.Error(), "mkdir") || strings.Contains(err.Error(), ".tmp") {
		t.Errorf("stats hands back the syscall: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot write the index") {
		t.Errorf("stats does not word the failure like the other paths: %v", err)
	}

	var out bytes.Buffer
	doctorIndexSection(t, &out, dir)
	line := ""
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "freshness") {
			line = l
		}
	}
	if !strings.Contains(line, "cannot be written") {
		t.Errorf("doctor calls an unwritable index ordinarily stale: %q", line)
	}
	if strings.Contains(line, "run `deja index`") {
		t.Errorf("doctor advises the command that fails in this state: %q", line)
	}

	// With the directory writable again the ordinary advice comes back.
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorIndexSection(t, &out, dir)
	if !strings.Contains(out.String(), "run `deja index`") {
		t.Errorf("a writable index lost the advice that fits it:\n%s", out.String())
	}
}

func doctorIndexSection(t *testing.T, out *bytes.Buffer, dir string) {
	t.Helper()
	if err := runDoctor(out, nil, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
}
