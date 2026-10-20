package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A batch of nothing but tool output imported 0 records under
// DEJA_INDEX_TOOL_OUTPUT=0, which read as an empty or already-synced batch
// (#5425). The line names the switch that kept them out.
func TestSyncImportSaysASwitchKeptRecordsOut(t *testing.T) {
	hermeticEnv(t)
	for _, k := range []string{"DEJA_INDEX_COMMANDS", "DEJA_INDEX_PATHS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"s1","project":"work/app","role":"tool-output","text":"zonkoerror: overflow","time":"2026-08-04T12:00:00Z","origin":"mini"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	out := captureStdout(t, func() { err = runSync(index.DefaultDir(), []string{"import", batch}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 record left out — switched off on this machine (DEJA_INDEX_TOOL_OUTPUT=0); to take them, unset it and import the batch again") {
		t.Errorf("the switched-off record is not reported:\n%s", out)
	}

	// Control: with the switch on the same record lands and no line is printed.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	out = captureStdout(t, func() { err = runSync(index.DefaultDir(), []string{"import", batch}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "switched off") || !strings.Contains(out, "imported 1 record") {
		t.Errorf("with the switch on:\n%s", out)
	}
}

// A pull has moved the peer's watermark past what a switch left out, so the
// line on that path names the full pull that sends it again.
func TestSwitchedOffLineNamesTheFullPullForAHost(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	batch := t.TempDir()
	line := `{"harness":"claude","session_id":"s1","project":"work/app","role":"command","text":"go test ./...","time":"2026-08-04T12:00:00Z","origin":"mini"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Import(index.DefaultDir(), batch); err != nil {
		t.Fatal(err)
	}
	if got := switchedOffLine("mini"); !strings.Contains(got, "deja sync ssh mini --pull --full") {
		t.Errorf("the pull's line: %q", got)
	}
}
