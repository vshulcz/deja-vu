package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every config deja writes holds an absolute path to the binary. Move it and
// the harness fails to start deja on every session — while doctor printed
// "wired" for a hook that cannot run. The repair from #773 runs from the hook
// path, which is the one path a dead binary cannot reach (#876).
func TestDoctorNamesConfigsPointingAtAMissingBinary(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tmp, "old", "deja")
	write := func(exe string) {
		b, err := json.Marshal(wiringState{Version: "dev", Targets: []string{"claude-auto"}, Exe: exe})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(gone)
	var out bytes.Buffer
	doctorWiringExe(&out)
	got := out.String()
	if !strings.Contains(got, gone) || !strings.Contains(got, "which is not there") {
		t.Errorf("doctor says nothing about a binary that moved:\n%s", got)
	}
	if !strings.Contains(got, "deja install claude-auto") {
		t.Errorf("doctor does not say how to fix it:\n%s", got)
	}

	// And the row reaches the command, not only the helper: a doctor that
	// never calls this is a doctor that still says "wired".
	full, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "which is not there") {
		t.Errorf("`deja doctor` does not mention the moved binary:\n%s", full)
	}

	// A binary that is where the configs say: nothing to report.
	here := filepath.Join(tmp, "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(here)
	out.Reset()
	doctorWiringExe(&out)
	if got := out.String(); got != "" {
		t.Errorf("a healthy wiring was reported as stale: %q", got)
	}

	// Nothing wired at all: no row either.
	b, err := json.Marshal(wiringState{Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorWiringExe(&out)
	if got := out.String(); got != "" {
		t.Errorf("an unwired machine got a wiring row: %q", got)
	}
}
