package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A store can be half-read: some sessions arrive, one file is refused. The line
// carried the refused *lines* and the missing-tool reason and said nothing
// about a refused file, so a store that gave up ten sessions and lost three
// tasks read exactly like a store with nothing wrong (#2236).
func TestAStoreNarratesTheFilesItRefusedBesideTheSessionsItRead(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().Add(-time.Hour).UnixMilli()
	good := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user",`+
		`"content":"the pool timed out during the migration"}]}`, stamp)
	if err := os.WriteFile(filepath.Join(cline, "good.messages.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cline, "bad.messages.json"), []byte(`[{"ts":1,`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: the store did produce a session, so the line exists and the
	// silence below is about the other file.
	if !strings.Contains(said, "cline: 1 session") {
		t.Fatalf("the readable session did not land, so this measures nothing:\n%s", said)
	}
	health := IngestHealth(dir)
	if health["cline"].FailedFiles != 1 {
		t.Fatalf("the refused file was not recorded, so this measures nothing: %v", health)
	}
	if !strings.Contains(said, "path") {
		t.Errorf("the line says nothing about the file it could not read:\n%s", said)
	}
}
