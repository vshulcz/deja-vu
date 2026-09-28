package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Every Claude Code hook exited 127 on a Windows machine whose settings named
// `H:\pycode\Self\deja-vu\deja.exe`, and doctor printed "wired": the binary
// was there, and Git Bash could not reach it as spelled (#4116). The same
// spelling fails in any bash, so the check is exercised here with it.
func TestDoctorSaysWhenTheShellCannotRunTheHook(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	hermeticEnv(t)
	write := func(exe string) {
		t.Helper()
		var b strings.Builder
		b.WriteString(`{"hooks":{`)
		for i, h := range claudeHookWiring {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + h.Event + `":[{"hooks":[{"type":"command","command":` + jsonQuoted(exe+" "+h.Sub) + `}]}]`)
		}
		b.WriteString(`}}`)
		dir := sources.ClaudeConfigDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(`H:\pycode\Self\deja-vu\deja.exe`)
	if st := claudeHookWiringState(); !st.dead {
		t.Errorf("a hook bash cannot run reads as %q and not dead", st.state)
	}
	if note := claudeHookRunNote(claudeHookWiringState().hooks); !strings.Contains(note, "exits 127") {
		t.Errorf("no note for a hook bash cannot run: %q", note)
	}
	if out, _ := captureRun(t, "doctor"); !strings.Contains(out, "every hook exits 127") {
		t.Errorf("doctor's report does not say the hooks cannot start:\n%s", out)
	}

	// The control: the same file naming a binary bash can run says nothing.
	ok := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(hookCommandQuote(ok))
	if note := claudeHookRunNote(claudeHookWiringState().hooks); note != "" {
		t.Errorf("a runnable hook was reported: %q", note)
	}
	if st := claudeHookWiringState(); st.dead {
		t.Error("a runnable hook reads as dead")
	}
}
