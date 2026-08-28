package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// share and promote refuse a session whose project the exclude list covers,
// because the pattern only runs at ingest. handoff packages that session for
// another agent and did not check at all (#2280).
func TestHandoffRefusesAnExcludedProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-secretproj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sec1","timestamp":%q,"cwd":"/work/secretproj",`+
		`"message":{"role":"user","content":"the payroll export for acme-prod drops rows"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: without a pattern the handoff packages the session.
	var out bytes.Buffer
	if err := runHandoff(dir, []string{"sec1"}, &out); err != nil {
		t.Fatalf("handoff without an exclude pattern: %v", err)
	}
	if !strings.Contains(out.String(), "payroll") {
		t.Fatalf("the handoff carried no session text: %q", out.String())
	}

	t.Setenv("DEJA_EXCLUDE_PROJECTS", "secretproj")
	out.Reset()
	err := runHandoff(dir, []string{"sec1"}, &out)
	if err == nil {
		t.Errorf("handoff of an excluded project succeeded, printing %q", out.String())
	} else if !strings.Contains(err.Error(), "exclude") {
		t.Errorf("handoff said %v — it should name the exclude list, as share does", err)
	}
	if strings.Contains(out.String(), "payroll") {
		t.Error("the refusal still printed the session's text")
	}
}
