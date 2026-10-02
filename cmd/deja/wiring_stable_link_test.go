package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Homebrew keeps each release in its own Cellar directory and moves the
// `bin/deja` link to the new one. The repair after an upgrade wrote the
// resolved Cellar path into every entry, including entries that named the link
// and still worked, so the next upgrade broke them again (#4189).
func TestWiringRepairKeepsTheLinkTheBinaryIsReachedBy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runningExe = os.Executable })
	runningExe = func() (string, error) { return link, nil }

	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	// The upgrade: the binary the record names is gone.
	writeWiringExe(t, filepath.Join(home, "Cellar", "deja-vu", "1.0", "bin", "deja"))

	refreshWiringAfterUpgrade()

	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), jsonString(link)) {
		t.Fatalf("the entry no longer names the link %s:\n%s", link, b)
	}
	if strings.Contains(string(b), jsonString(real)) {
		t.Fatalf("the entry was pinned to the resolved binary %s:\n%s", real, b)
	}
}

// Linux reports the resolved binary as the executable, so the link has to be
// found where the installers leave one.
func TestWiringRepairFindsTheLinkWhenTheExecutableIsResolved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	real, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := stableExe(real, real); got != link {
		t.Fatalf("stableExe = %s, want the link %s", got, link)
	}
	// A link to some other binary is not this one.
	other := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if got := stableExe(real, real); got != real {
		t.Fatalf("stableExe = %s, want the binary itself %s", got, real)
	}
}

// A link in a temp directory is not a stable path: the repair would trade a
// binary that stays for a link that goes with the directory.
func TestWiringRepairSkipsALinkInATempDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	real, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	link := filepath.Join(home, "scratch", "tmp", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	was := exeIsTemporary
	t.Cleanup(func() { exeIsTemporary = was })
	exeIsTemporary = func(p string) bool {
		return strings.Contains(p, string(filepath.Separator)+"tmp"+string(filepath.Separator))
	}
	if got := stableExe(link, real); got != real {
		t.Fatalf("stableExe = %s, want the binary %s rather than a temp link", got, real)
	}
}

// The configs name the link; remove it and they are dead while the binary is
// still where the record says, so the repair has to look at the link too.
func TestWiringRepairNoticesTheLinkItWroteIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	link := filepath.Join(home, ".local", "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runningExe = os.Executable })
	runningExe = func() (string, error) { return link, nil }
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if st := readWiringState(); st.Written != link || st.Exe != real {
		t.Fatalf("record exe=%s written=%s, want %s through %s", st.Exe, st.Written, real, link)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	runningExe = func() (string, error) { return real, nil }
	refreshWiringAfterUpgrade()

	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), jsonString(real)) {
		t.Fatalf("the entry still names the removed link:\n%s", b)
	}
	if st := readWiringState(); st.Written != "" {
		t.Fatalf("record still names a link: %q", st.Written)
	}
}
