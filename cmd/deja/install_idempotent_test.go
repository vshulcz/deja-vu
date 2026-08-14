package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Installing twice must leave what installing once left. It did not: the entry
// deja creates had no statusMessage while the entry it adopts on a second run
// did, so a first install wrote one file and a reinstall wrote another — and
// whoever installed once never saw the line their harness shows while the hook
// runs. Found by sweeping install-then-install across the harnesses.
func TestInstallIsIdempotent(t *testing.T) {
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			const exe = "/usr/local/bin/deja"
			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("first install: %v", err)
			}
			first := snapshotHome(t, home)
			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("second install: %v", err)
			}
			second := snapshotHome(t, home)

			for path, a := range first {
				b, ok := second[path]
				if !ok {
					t.Errorf("%s disappeared on reinstall", path)
					continue
				}
				if a != b {
					t.Errorf("%s differs between the first install and the second:\n--- first\n%s\n--- second\n%s",
						path, a, b)
				}
			}
			for path := range second {
				if _, ok := first[path]; !ok {
					t.Errorf("%s appeared only on the second install", path)
				}
			}
		})
	}
}

func snapshotHome(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".bak") {
			return nil
		}
		// deja's own state records when it last wired, which is allowed to
		// differ between two runs.
		if strings.Contains(path, filepath.Join(".config", "deja")) {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			rel = path
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
