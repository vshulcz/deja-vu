package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A ZCode snapshot is one file per conversation, rewritten whole as it grows,
// so a pass that re-reads it replaces what it held. Records from it were taken
// for the CLI database's, whose rows a pass does not drop by path, and a
// rewritten snapshot came back on top of its old copy (#4432).
func TestARewrittenZCodeSnapshotIsHeldOnce(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_ZCODE_ROOT", filepath.Join(tmp, "absent"))
	t.Setenv("DEJA_ZCODE_DB", filepath.Join(tmp, "absent.sqlite"))
	root := filepath.Join(tmp, "zcode-v2")
	t.Setenv("DEJA_ZCODE_LEGACY_ROOT", root)
	snap := filepath.Join(root, "5f0c1e9a", "task-1.json")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatal(err)
	}
	msgs := []string{
		`{"role":"user","content":"fix the retry loop, it never stops","timestamp":1790000000000}`,
		`{"role":"assistant","content":"capped it at five attempts","timestamp":1790000060000}`,
	}
	write := func(n int, at time.Time) {
		t.Helper()
		body := `{"meta":{"taskId":"task-1","acpSessionId":"acp-9","workspacePath":"/private/tmp/proj","title":"retry","createdAt":1790000000000},"messages":[` +
			strings.Join(msgs[:n], ",") + `]}`
		if err := os.WriteFile(snap, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(snap, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(1, time.Now().Add(-time.Hour))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		s, ok, err := FindByIdentity(dir, "zcode", "acp-9")
		if err != nil || !ok {
			t.Fatalf("zcode:acp-9 is not in the index (%v)", err)
		}
		return len(s.Messages)
	}
	if got := count(); got != 1 {
		t.Fatalf("the build holds %d messages, so this measures nothing", got)
	}

	write(2, time.Now())
	var out strings.Builder
	if err := Ensure(dir, "", false, &out); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 2 {
		t.Errorf("after the snapshot grew by one turn it holds %d messages, want 2 (%s)", got, out.String())
	}
}
