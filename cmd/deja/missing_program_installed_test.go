package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Installed since: the sightings stay on file and the line went on saying the
// program was missing. On one machine all 56 of these for docker and shellcheck
// were shown after the binary was there.
func TestAProgramOnPathIsNotCalledMissing(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := seedMissingProgram(t, "launchctl kickstart -k system/app.worker")
	cmd := "timeout 30 launchctl kickstart -k system/app.worker"
	if got := missingProgramLine(dir, cmd); !strings.Contains(got, "timeout is not on this machine") {
		t.Fatalf("fixture: no line while timeout is absent: %q", got)
	}
	name := "timeout"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := missingProgramLine(dir, cmd); got != "" {
		t.Errorf("timeout is on PATH and the line still says it is missing: %q", got)
	}
}
