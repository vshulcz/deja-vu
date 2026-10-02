package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// aider's config points every launch at one context file. `deja aider` filled
// it with the digest of the directory it ran in and left it there, so plain
// aider in another project read that project's sessions under a lead calling
// them "this project's" — live, the model answered "we previously fixed
// retry.py" from it (#4328).
func TestDejaAiderLeavesNoDigestForPlainAider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in aider is a shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_WARMUP_SENTINEL", "1")
	t.Setenv("DEJA_HOOK_REFRESH", "1")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := filepath.Join(home, "index")
	projA := filepath.Join(home, "proj-a")
	for _, d := range []string{dir, projA} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(projA)
	cwd, _ := os.Getwd()
	writeHookCache(dir, cwd, "  - Session: **proj-a** `aider-1`\n", 1, 10, nil, 0, []string{"aider-1"}, []string{"proj-a"})

	// The stand-in aider keeps a copy of what it was handed.
	bin := filepath.Join(home, "bin")
	seen := filepath.Join(home, "seen.md")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncp '" + aiderContextPath() + "' '" + seen + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "aider"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdAider(dir, []string{"--exit"}, ""); err != nil {
		t.Fatal(err)
	}
	during, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(during), "aider-1") {
		t.Fatalf("the aider deja started was not handed the digest:\n%s", during)
	}
	// The lead names the directory the digest was built for, rather than
	// calling it whatever project the reader is in.
	if !strings.Contains(string(during), cwd) || strings.Contains(string(during), "this project's recent history") {
		t.Errorf("the lead does not name the project it was built for:\n%s", during)
	}
	after, err := os.ReadFile(aiderContextPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "aider-1") {
		t.Errorf("plain aider started next, anywhere, reads proj-a's sessions:\n%s", after)
	}
	if strings.TrimSpace(string(after)) == "" {
		t.Error("the file must stay non-empty; aider refuses to start without it")
	}
}
