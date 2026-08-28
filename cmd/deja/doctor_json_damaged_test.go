package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The human doctor names a damaged index (#735) and the JSON called the same
// store "ok", so a script watching it for index health was told the store was
// fine while it could not answer a query (#2292).
func TestDoctorJSONReportsADamagedIndex(t *testing.T) {
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
	t.Setenv("DEJA_INDEX_DIR", dir)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"gateway_timeout"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	state := func() string {
		t.Helper()
		out, err := captureRun(t, "doctor", "--json", "--offline")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Index struct {
				State string `json:"state"`
			} `json:"index"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("doctor --json is not JSON: %v", err)
		}
		return report.Index.State
	}

	// The premise: a healthy index reports ok.
	if got := state(); got != "ok" {
		t.Fatalf("a fresh index reports %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The premise for the second half: deja itself considers this damaged.
	if !index.Damaged(dir) {
		t.Fatal("the store is not damaged, so this measures nothing")
	}
	if got := state(); got == "ok" {
		t.Error("the JSON calls a damaged index ok")
	} else if !strings.Contains(got, "damaged") {
		t.Errorf("the JSON reports %q, which does not say the index is damaged", got)
	}
}
