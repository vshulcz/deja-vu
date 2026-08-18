package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func roleStore(t *testing.T, withTool bool) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"why does the build fail"}}` + "\n"
	if withTool {
		lines += `{"type":"user","sessionId":"a","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","content":"go build ./... failed"}]}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(proj, "a.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The question behind "this index holds no tool records at all": absent means
// absent, present means present, and an index that cannot be read says nothing
// rather than claiming absence.
func TestHasRecordOfRole(t *testing.T) {
	if dir := roleStore(t, true); !HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a store with a tool result reports none")
	}
	if dir := roleStore(t, false); HasRecordOfRole(dir, roleToolOutput) {
		t.Error("a talk-only store reports tool records")
	}
	if !HasRecordOfRole(filepath.Join(t.TempDir(), "no-index"), roleToolOutput) {
		t.Error("an unreadable index claimed the role is absent")
	}
}

// It stops at the first match rather than reading the whole log: the caller
// asks on every empty role search, and most stores that have the records have
// them near the front.
func TestHasRecordOfRoleStopsEarly(t *testing.T) {
	dir := roleStore(t, true)
	started := time.Now()
	for i := 0; i < 50; i++ {
		if !HasRecordOfRole(dir, roleToolOutput) {
			t.Fatal("lost the record it just found")
		}
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("fifty existence checks took %v", took)
	}
}
