package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// stubWiredVersions makes the version reader answer from a table, so a test
// can stand up "an older deja" without building one.
func stubWiredVersions(t *testing.T, running string, table map[string]string) {
	t.Helper()
	savedV, savedR := version, dejaBinaryVersion
	t.Cleanup(func() { version, dejaBinaryVersion = savedV, savedR })
	version = running
	dejaBinaryVersion = func(p string) string { return table[p] }
}

func writeExe(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeWiring(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Wiring that names an older build in full is the plain case: the row said
// `wired`, and every fix since that build was missing from the sessions. Two
// harnesses running the same build are one finding, not two.
func TestOlderWiredBinariesGroupsAnOlderBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Join(dir, "empty"))
	old := filepath.Join(dir, "old", "deja")
	writeExe(t, old)
	stubWiredVersions(t, "0.21.5", map[string]string{old: "0.21.3"})

	hooks := filepath.Join(dir, "hooks.json")
	writeWiring(t, hooks, `{"command":"`+old+` hook-context"}`)
	mcp := filepath.Join(dir, "mcp.json")
	writeWiring(t, mcp, `{"mcpServers":{"deja":{"command":"`+old+`","args":["mcp"]}}}`)
	files := []wiredFile{{"codex-hook", hooks}, {"cursor", mcp}, {"cursor", mcp}}

	got := olderWiredBinaries(files)
	if len(got) != 1 {
		t.Fatalf("want one older binary, got %+v", got)
	}
	if got[0].path != old || got[0].version != "0.21.3" || strings.Join(got[0].names, ",") != "codex-hook,cursor" {
		t.Errorf("unexpected finding: %+v", got[0])
	}

	// The same version, a newer one, or a source build on either side: quiet.
	for _, c := range []struct{ running, wired string }{
		{"0.21.5", "0.21.5"}, {"0.21.5", "0.22.0"}, {"dev", "0.21.3"}, {"0.21.5", ""},
	} {
		stubWiredVersions(t, c.running, map[string]string{old: c.wired})
		if got := olderWiredBinaries(files); len(got) != 0 {
			t.Errorf("running %s, wired %q: unexpected finding %+v", c.running, c.wired, got)
		}
	}
}

// The launcher is what every hook names, so the question is which binary it
// picks — in the order the installed script lists them, which is how a stale
// ~/.local/bin build kept winning over a newer Homebrew one.
func TestOlderWiredBinariesFollowsTheLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))
	t.Setenv("DEJA_BIN", "")
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	newer := filepath.Join(tmp, "brew", "deja")
	launcherWellKnown = []string{newer}

	older := filepath.Join(tmp, "local", "deja")
	writeExe(t, older)
	writeExe(t, newer)
	if _, err := writeDejaLauncher(older); err != nil {
		t.Fatal(err)
	}
	stubWiredVersions(t, "0.21.5", map[string]string{older: "0.21.3", newer: "0.21.5"})

	wiring := filepath.Join(tmp, "settings.json")
	writeWiring(t, wiring, `{"command":"`+dejaLauncherPath()+` hook-prompt"}`)
	files := []wiredFile{{"claude-code", wiring}}
	if got := olderWiredBinaries(files); len(got) != 1 || got[0].path != older {
		t.Fatalf("the launcher's pick was not named: %+v", got)
	}

	// DEJA_BIN wins in the script, so it wins here.
	t.Setenv("DEJA_BIN", newer)
	if got := olderWiredBinaries(files); len(got) != 0 {
		t.Errorf("DEJA_BIN points at the current build, yet: %+v", got)
	}
	t.Setenv("DEJA_BIN", "")

	// The old one gone, the launcher falls through to the current build.
	if err := os.Remove(older); err != nil {
		t.Fatal(err)
	}
	if got := olderWiredBinaries(files); len(got) != 0 {
		t.Errorf("the launcher now runs the current build, yet: %+v", got)
	}
}

// An MCP entry naming a bare `deja` runs the PATH's, and a package manager's
// binary gets that manager's command rather than `deja update`, which would
// refuse it.
func TestDoctorOlderBinariesBarePathAndManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	tmp := hermeticEnv(t)
	keg := filepath.Join(tmp, "Cellar", "deja-vu", "0.18.0", "bin")
	writeExe(t, filepath.Join(keg, "deja"))
	t.Setenv("PATH", keg)
	t.Setenv("DEJA_BIN", "")
	stubWiredVersions(t, "0.21.5", map[string]string{filepath.Join(keg, "deja"): "0.18.0"})

	cursor := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursor), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWiring(t, cursor, `{"mcpServers":{"deja":{"command":"deja","args":["mcp"]}}}`)
	got := olderWiredBinaries([]wiredFile{{"cursor", cursor}})
	if len(got) != 1 || got[0].version != "0.18.0" {
		t.Fatalf("bare deja on PATH was not followed: %+v", got)
	}

	var out bytes.Buffer
	doctorOlderBinaries(&out)
	line := out.String()
	for _, want := range []string{"v0.18.0", "v0.21.5", "brew upgrade deja-vu"} {
		if !strings.Contains(line, want) {
			t.Errorf("doctor row lacks %q: %s", want, line)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("one older binary should be one row:\n%s", line)
	}
}

// The reader runs a binary only when its build info says it is deja-vu's: a
// config can name anything, and doctor must not execute it on that say-so.
func TestDejaBinaryVersionNeverRunsAStranger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	p := filepath.Join(dir, "deja")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntouch "+marker+"\necho deja 9.9.9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := readDejaBinaryVersion(p); v != "" {
		t.Errorf("a shell script was read as deja %s", v)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("doctor executed a binary that is not deja's")
	}
}
