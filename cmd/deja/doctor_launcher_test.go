package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// The check beside this one asks whether the file a hook names is on disk.
// Since the launcher landed that file is always there, so the question moved:
// what can be gone is everything the launcher resolves to. A machine in that
// state has hooks that start, find nothing and exit 0 — silence that reads as
// having no history (#3422).
func TestDoctorSaysWhenTheLauncherFindsNoDeja(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	// Nothing on the PATH and no well-known copy, so the launcher has only
	// what the record names.
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))
	t.Setenv("DEJA_BIN", "")
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	launcherWellKnown = nil

	gone := filepath.Join(tmp, "old", "deja")
	writeWiringFixture(t, wiringState{Version: "dev", Targets: []string{"claude-code"}, Exe: gone, Home: homeDir()})
	if _, err := writeDejaLauncher(gone); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": dejaLauncherPath() + " hook-context"},
		}}},
	}}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHooks(&out)
	if got := out.String(); !strings.Contains(got, "finds no deja to run") {
		t.Errorf("doctor said nothing about a launcher that resolves to nothing:\n%s", got)
	}

	// Put a binary where the record says, and the row goes quiet again.
	if err := os.MkdirAll(filepath.Dir(gone), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gone, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if got := out.String(); strings.Contains(got, "finds no deja to run") {
		t.Errorf("doctor complained about a launcher that resolves:\n%s", got)
	}
}

func TestDoctorReportsABinaryThatFailsToStart(t *testing.T) {
	tmp := hermeticEnv(t)
	savedWK := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = savedWK })
	launcherWellKnown = nil
	t.Setenv("DEJA_BIN", "")
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))

	home := os.Getenv("HOME")
	broken := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	launcher := dejaLauncherPath()
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte(dejaLauncherScript(broken)), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(cfg, []byte(launcher), 0o644); err != nil {
		t.Fatal(err)
	}

	healthy := filepath.Join(home, "go", "bin", "deja")
	origRunErr := dejaBinaryRunErr
	t.Cleanup(func() { dejaBinaryRunErr = origRunErr })
	dejaBinaryRunErr = func(p string) string {
		if p == broken {
			return "signal: killed"
		}
		return ""
	}

	if got := doctorLauncherNote(cfg, "claude"); !strings.Contains(got, "fails to start") {
		t.Errorf("all-broken note = %q, want substring %q", got, "fails to start")
	}

	// With a healthy fallback and the resilient launcher, doctor stays quiet.
	if err := os.MkdirAll(filepath.Dir(healthy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(healthy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := doctorLauncherNote(cfg, "claude"); got != "" {
		t.Errorf("resilient launcher with healthy fallback note = %q, want empty", got)
	}
	if resolved, ok := launcherResolves(); !ok || resolved != healthy {
		t.Errorf("launcherResolves() = (%q, %v), want (%q, true)", resolved, ok, healthy)
	}

	// Legacy exec launcher picks broken first candidate even when fallback exists.
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec \"$c\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := doctorLauncherNote(cfg, "claude"); !strings.Contains(got, "fails to start") {
		t.Errorf("legacy launcher + broken primary note = %q, want substring %q", got, "fails to start")
	}
}

func TestDejaBinaryRunErrNeverRunsAStranger(t *testing.T) {
	tmp := t.TempDir()
	stranger := filepath.Join(tmp, "deja")
	sentinel := filepath.Join(tmp, "ran")
	if err := os.WriteFile(stranger, []byte("#!/bin/sh\ntouch "+shellQuote(sentinel)+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := probeDejaBinaryRunErr(stranger); got != "" {
		t.Fatalf("probeDejaBinaryRunErr(stranger) = %q, want empty (strangers are never executed)", got)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("probeDejaBinaryRunErr executed a non-deja script")
	}
}
