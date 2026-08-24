package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The wiring names the binary that installed it. Move that binary — a go
// install into a different GOBIN, a brew prefix change — and every hook exits
// 127 on every prompt while doctor reports the machine as wired (#1708).
func TestDoctorNamesAVanishedBinary(t *testing.T) {
	hermeticEnv(t)
	gone := filepath.Join(t.TempDir(), "bin", "deja")
	recordWiringExe(gone)

	var buf bytes.Buffer
	doctorWiredExe(&buf)
	out := buf.String()
	if !strings.Contains(out, gone) {
		t.Errorf("doctor does not name the binary the wiring points at:\n%s", out)
	}
	if !strings.Contains(out, "deja install") {
		t.Errorf("doctor does not say what to run:\n%s", out)
	}
}

// The control: a binary that is where the wiring says says nothing at all.
func TestDoctorSaysNothingWhenTheBinaryIsThere(t *testing.T) {
	hermeticEnv(t)
	here := filepath.Join(t.TempDir(), "deja")
	if err := os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	recordWiringExe(here)

	var buf bytes.Buffer
	doctorWiredExe(&buf)
	if out := buf.String(); out != "" {
		t.Errorf("doctor complained about a binary that is present:\n%s", out)
	}
}

// And a machine that has never installed says nothing either.
func TestDoctorSaysNothingWithoutWiring(t *testing.T) {
	hermeticEnv(t)
	var buf bytes.Buffer
	doctorWiredExe(&buf)
	if out := buf.String(); out != "" {
		t.Errorf("doctor complained with no wiring recorded:\n%s", out)
	}
}

// recordWiringExe writes a wiring record naming exe, the way an install would.
func recordWiringExe(exe string) {
	st := wiringState{Version: version, Targets: []string{"claude-code"}, Exe: exe, Home: homeDir()}
	b, _ := json.MarshalIndent(st, "", "  ")
	path := wiringStatePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o600)
}
