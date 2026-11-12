package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnderEphemeralRunnerAndTempDir(t *testing.T) {
	ephemeral := []string{
		"/Users/alex/.npm/_npx/9a8b7c6d/node_modules/@vshulcz/deja-vu-darwin-arm64/bin/deja",
		"/home/alex/.cache/pnpm/dlx/abc123/node_modules/.bin/deja",
		"/Users/alex/Library/Caches/pnpm/dlx/12345/deja",
		"/home/alex/.yarn/berry/dlx-48291/deja",
		"/Users/alex/.bun/install/cache/bunx-1001-@vshulcz/deja-vu/bin/deja",
	}
	for _, p := range ephemeral {
		if !underEphemeralRunner(p) {
			t.Errorf("underEphemeralRunner(%q) = false, want true", p)
		}
		if !underTempDir(p) {
			t.Errorf("underTempDir(%q) = false, want true", p)
		}
	}

	permanent := []string{
		"/Users/alex/.local/bin/deja",
		"/opt/homebrew/bin/deja",
		"/usr/local/lib/node_modules/@vshulcz/deja-vu/bin/deja",
		"/Users/alex/.nvm/versions/node/v22.0.0/bin/deja",
	}
	for _, p := range permanent {
		if underEphemeralRunner(p) {
			t.Errorf("underEphemeralRunner(%q) = true, want false", p)
		}
	}
}

func TestStageEphemeralExeCopiesToLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	npxDir := filepath.Join(home, ".npm", "_npx", "deadbeef", "bin")
	if err := os.MkdirAll(npxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(npxDir, "deja")
	payload := []byte("#!/bin/sh\necho staged\n")
	if err := os.WriteFile(src, payload, 0o755); err != nil {
		t.Fatal(err)
	}

	staged, err := stageEphemeralExe(src)
	if err != nil {
		t.Fatalf("stageEphemeralExe: %v", err)
	}
	wantName := "deja"
	if runtime.GOOS == "windows" {
		wantName = "deja.exe"
	}
	want := filepath.Join(home, ".local", "bin", wantName)
	if staged != want {
		t.Fatalf("staged = %q, want %q", staged, want)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged binary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("staged bytes = %q, want %q", got, payload)
	}

	// If a permanent binary already exists at ~/.local/bin/deja, staging does
	// not clobber it.
	existing := []byte("#!/bin/sh\necho existing\n")
	if err = os.WriteFile(want, existing, 0o755); err != nil {
		t.Fatal(err)
	}
	staged2, err := stageEphemeralExe(src)
	if err != nil {
		t.Fatalf("stageEphemeralExe second run: %v", err)
	}
	if staged2 != want {
		t.Fatalf("staged2 = %q, want %q", staged2, want)
	}
	got2, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got2, existing) {
		t.Fatalf("existing binary was overwritten: %q", got2)
	}
}
