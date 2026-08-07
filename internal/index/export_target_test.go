package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pointing sync export at a file is the same mistake as pointing sync import at
// one, and only the import side said so; export handed back the raw
// `mkdir /…: not a directory` (#1112).
func TestExportToAFileSaysWhatItWanted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(tmp, "claude", "-tmp-p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"a decision"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Export(dir, target)
	if err == nil {
		t.Fatal("export accepted a file as its output directory")
	}
	if !strings.Contains(err.Error(), "is a file; sync export wants a directory") {
		t.Errorf("raw error came back: %v", err)
	}
}
