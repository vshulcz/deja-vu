package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// aiderStandIn sets up a home with a digest for its project and an `aider` on
// PATH running script, and returns the index dir.
func aiderStandIn(t *testing.T, home, script string) string {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := filepath.Join(home, "index")
	proj := filepath.Join(home, "proj")
	bin := filepath.Join(home, "bin")
	for _, d := range []string{dir, proj, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(proj)
	cwd, _ := os.Getwd()
	writeHookCache(dir, cwd, "  - Session: **proj** `aider-1`\n", 1, 10, nil, 0, []string{"aider-1"}, []string{"proj"})
	if err := os.WriteFile(filepath.Join(bin, "aider"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func waitForFile(t *testing.T, p string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Fatalf("%s never appeared", p)
}

// Two `deja aider` in one project write the same digest. The first to exit
// found the file still holding it and put the placeholder back, and the aider
// still running lost its recall mid-session (#4328).
func TestDejaAiderKeepsTheDigestWhileAnotherRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script")
	}
	home := t.TempDir()
	started, release := filepath.Join(home, "started"), filepath.Join(home, "release")
	// With an argument the stand-in waits to be released; without, it exits.
	dir := aiderStandIn(t, home, "[ \"$1\" = wait ] || exit 0\ntouch '"+started+"'\nwhile [ ! -e '"+release+"' ]; do sleep 0.05; done\n")
	done := make(chan error, 1)
	go func() { done <- cmdAider(dir, []string{"wait"}, "") }()
	waitForFile(t, started)
	if err := cmdAider(dir, []string{"--exit"}, ""); err != nil {
		t.Fatal(err)
	}
	during, _ := os.ReadFile(aiderContextPath())
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(during), "aider-1") {
		t.Errorf("the aider still running lost its digest when the other exited:\n%s", during)
	}
	if after, _ := os.ReadFile(aiderContextPath()); strings.TrimSpace(string(after)) != strings.TrimSpace(aiderPlaceholder) {
		t.Errorf("the last one out left:\n%q", after)
	}
}
