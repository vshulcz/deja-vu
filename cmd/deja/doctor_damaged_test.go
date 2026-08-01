package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// doctor's job is answering "is my setup right", and it said "built, up to
// date" about a store that could not return a single result (#735).
func TestDoctorReportsADamagedIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"s1","cwd":"/w/p","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorIndex(&buf, doctorComponent{State: "fresh", Path: dir}, dir)
	if strings.Contains(buf.String(), "integrity") {
		t.Errorf("healthy index reported damage:\n%s", buf.String())
	}

	if err := os.RemoveAll(filepath.Join(dir, "buckets")); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorIndex(&buf, doctorComponent{State: "fresh", Path: dir}, dir)
	out := buf.String()
	if !strings.Contains(out, "integrity damaged") {
		t.Errorf("damaged index reported as healthy:\n%s", out)
	}
	// The reader has not lost memory, only this build — say so, or the line
	// reads as data loss.
	if !strings.Contains(out, "the next search rebuilds") {
		t.Errorf("no recovery named:\n%s", out)
	}
	// "up to date" must not sit under a damage line.
	if strings.Contains(out, "freshness up to date") {
		t.Errorf("still claims freshness:\n%s", out)
	}
}
