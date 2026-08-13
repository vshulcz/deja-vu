package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Config files are very often symlinks into a dotfiles repository. Writing
// through one by rename replaces the link with a regular file: the user's repo
// stops tracking their config, and the next `stow`/`chezmoi` run either
// clobbers deja's wiring or reports a conflict. What deja writes has to land in
// the file the link points at, with the link still a link.
func TestInstallWritesThroughASymlink(t *testing.T) {
	for _, tc := range []struct{ target, rel string }{
		{"qwen-auto", ".qwen/settings.json"},
		{"codex-auto", ".codex/hooks.json"},
		{"goose-auto", ".config/goose/config.yaml"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			// The dotfiles repo, and the config linked into place from it.
			repo := filepath.Join(home, "dotfiles")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(repo, filepath.Base(tc.rel))
			if err := os.WriteFile(real, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(home, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			if _, err := installTarget(tc.target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}

			fi, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s is no longer a symlink; the dotfiles repo has lost the file", tc.rel)
			}
			b, err := os.ReadFile(real)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "/usr/local/bin/deja") {
				t.Fatalf("the wiring did not reach the file the link points at:\n%s", b)
			}
		})
	}
}
