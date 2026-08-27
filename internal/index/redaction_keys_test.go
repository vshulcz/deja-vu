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

// `deja stats` prints redaction counts grouped by these keys, so a cline user
// read a heading — "cline-sdk" — that no other screen uses. #2234 fixed the
// same shape for ingest_health and left this counter alone (#2238).
func TestRedactionCountsAreFiledUnderTheStore(t *testing.T) {
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

	secret := "ghp_" + strings.Repeat("a", 36)
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user","content":"the token is %s"}]}`, stamp, secret)
	if err := os.WriteFile(filepath.Join(cline, "one.messages.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the token is %s"}}`, at, secret)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: both stores had something redacted, or the names prove
	// nothing.
	if len(stats.Rules) < 2 {
		t.Fatalf("only %d store reported a redaction: %v", len(stats.Rules), stats.Rules)
	}
	for name := range stats.Rules {
		if sources.HarnessForKind(name) != name {
			t.Errorf("redactions are filed under %q, which is a file kind; every screen says %q",
				name, sources.HarnessForKind(name))
		}
	}
	if _, ok := stats.Rules["cline"]; !ok {
		t.Errorf("nothing is filed under the name the rest of deja prints: %v", stats.Rules)
	}
}
