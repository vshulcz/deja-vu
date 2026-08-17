package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A manifest.gob that will not decode reads as "built, up to date" to doctor,
// which stats the file rather than parsing it, while the next search throws the
// store away and rebuilds it.
func TestDamagedUnreadableManifest(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the retry storm"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if Damaged(dir) {
		t.Fatal("a freshly built index reported damage")
	}

	path := filepath.Join(dir, "manifest.gob")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8 {
		t.Fatalf("manifest too small to corrupt: %d bytes", len(data))
	}
	garbled := append([]byte{0xde, 0xad, 0xbe, 0xef}, data[4:]...)
	if err := os.WriteFile(path, garbled, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(dir); err == nil {
		t.Fatal("corrupted manifest still decoded; the test no longer corrupts anything")
	}
	if !Damaged(dir) {
		t.Error("an unreadable manifest was not reported as damage")
	}
}
