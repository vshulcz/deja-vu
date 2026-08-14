package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstalling something that was never installed must do nothing at all. The
// uninstall path computes "the file without deja in it", and for a file that is
// not there that is an empty config — which deja then created, so `deja
// uninstall --all` on a machine that never had it wrote configs for harnesses
// the user does not even use. Fixed once for the targets that showed it; this
// holds every target to it.
func TestUninstallOnACleanHomeCreatesNothing(t *testing.T) {
	for _, target := range installTargetNames() {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			removingWiring = true
			_, err := installTarget(target, "/usr/local/bin/deja", true)
			guidanceErr := func() error {
				_, gerr := guidanceResult(target, true)
				return gerr
			}()
			removingWiring = false
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if guidanceErr != nil {
				t.Fatalf("guidance uninstall: %v", guidanceErr)
			}

			var made []string
			_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, werr error) error {
				if werr != nil || d.IsDir() {
					return nil
				}
				// deja's own state is allowed: it records that an uninstall ran.
				if strings.Contains(path, filepath.Join(".config", "deja")) {
					return nil
				}
				rel, rerr := filepath.Rel(home, path)
				if rerr != nil {
					rel = path
				}
				made = append(made, rel)
				return nil
			})
			if len(made) > 0 {
				body, _ := os.ReadFile(filepath.Join(home, made[0]))
				t.Errorf("uninstalling on a clean home created %v; %s contains:\n%s", made, made[0], body)
			}
		})
	}
}
