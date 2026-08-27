package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The run narrates a store and doctor filed the same fact under the file kind —
// "cline-sdk" where the screen said "cline" — while the documentation calls the
// key a harness. A reader carrying a number from one to the other had to know a
// mapping deja never showed them (#2234).
func TestIngestHealthIsKeyedByTheStoreTheRunNames(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	t.Setenv("DEJA_CLINE_ROOT", cline)

	proj := filepath.Join(claude, "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	good := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the pool timed out during the migration"}}`, stamp)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(good+"\nnot json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cline, "1234.messages.json"), []byte(`[{"ts":1,`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	health := IngestHealth(dir)
	// The premise: both stores are in there, or the names below prove nothing.
	if len(health) < 2 {
		t.Fatalf("only %d store reported anything:\n%s\n%v", len(health), out.String(), health)
	}
	for name := range health {
		if sources.HarnessForKind(name) != name {
			t.Errorf("doctor files this under %q, which is a file kind; the run says %q",
				name, sources.HarnessForKind(name))
		}
	}
	// And the store the run named is findable by that name.
	if _, ok := health["cline"]; !ok {
		t.Errorf("nothing is filed under the name the run printed: %v", health)
	}
}
