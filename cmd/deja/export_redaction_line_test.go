package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The one line deja prints about secrets leaving the machine said records were
// redacted whatever the count was, so an export with nothing masked announced a
// redaction that never happened (#2255).
func TestTheExportLineDoesNotClaimARedactionThatDidNotHappen(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")

	write := func(name, body string) {
		t.Helper()
		at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":%q}}`, name, at, body)
		if err := os.WriteFile(filepath.Join(claude, name+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing in this session matches a redaction pattern.
	write("clean", "gateway_timeout on the reconnect_loop")
	out := filepath.Join(tmp, "out-clean")
	said := captureStderr(t, func() {
		if err := runSync(dir, []string{"export", out}); err != nil {
			t.Fatal(err)
		}
	})
	// The premise: something was exported, or the line is not printed at all.
	if !strings.Contains(said, "0 masked") && !strings.Contains(said, "nothing matched") {
		t.Logf("stderr was: %s", said)
	}
	if strings.Contains(said, "records were redacted") {
		t.Errorf("nothing was masked and the export said records were redacted:\n%s", said)
	}
	if !strings.Contains(said, "review the export") {
		t.Errorf("the caution is the point of the line and it is gone:\n%s", said)
	}

	// And with a secret in the store, the sentence is true and stays.
	write("dirty", "the token is ghp_"+strings.Repeat("a", 36))
	out2 := filepath.Join(tmp, "out-dirty")
	said = captureStderr(t, func() {
		if err := runSync(dir, []string{"export", out2, "--full"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(said, "records were redacted") {
		t.Errorf("a redacted store no longer says so:\n%s", said)
	}
}
