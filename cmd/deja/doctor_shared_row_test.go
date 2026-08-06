package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `2 files, 1 indexed session` has two causes that read the same: a file that
// failed to parse, and two files sharing an id. The manifest knows which, and
// the row did not say (#1101).
func TestDoctorSaysWhenARowCoversTwoTranscripts(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"a.jsonl", "b.jsonl"} {
		rec := `{"type":"user","message":{"role":"user","content":"conversation ` + name + `"},"timestamp":"2026-08-0` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"dup1","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHarnesses(&out, dir)
	row := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "claude ") {
			row = line
		}
	}
	if !strings.Contains(row, "2 files, 1 indexed session") {
		t.Fatalf("the premise changed: %q", row)
	}
	if !strings.Contains(row, "shared by two transcripts") {
		t.Errorf("the row does not say why the counts differ: %q", row)
	}
}
