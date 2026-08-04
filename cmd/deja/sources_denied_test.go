package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `deja sources` is the command the empty-machine advice names, and a store
// deja is not allowed to read looked exactly like one nobody has used —
// `sessions=0 messages=0 size=0 B` and nothing else (#1000).
func TestSourcesSaysWhenAStoreCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen")
	projects := filepath.Join(qwen, "projects", "p1")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "s.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", qwen)

	out, err := captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	if line := storeLine(out, "qwen"); strings.Contains(line, "cannot be read") {
		t.Fatalf("a readable store was reported as locked: %q", line)
	}

	if err := os.Chmod(filepath.Join(qwen, "projects"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(qwen, "projects"), 0o755) })

	out, err = captureRun(t, "sources")
	if err != nil {
		t.Fatal(err)
	}
	line := storeLine(out, "qwen")
	if !strings.Contains(line, "cannot be read") {
		t.Errorf("a locked store reads as an empty one: %q", line)
	}
	if !strings.Contains(line, "permission denied") {
		t.Errorf("the line does not say why: %q", line)
	}
	// Every other store keeps its plain row.
	if l := storeLine(out, "claude"); strings.Contains(l, "cannot be read") {
		t.Errorf("an untouched store picked up the note: %q", l)
	}
}

func storeLine(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, name+"\t") {
			return l
		}
	}
	return ""
}
