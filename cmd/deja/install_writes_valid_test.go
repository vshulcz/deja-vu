package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every target, installed into an empty home, must leave behind files their
// harness can actually read. Driving the real interfaces turned up the failure
// this guards: what deja writes is valid on its own terms and rejected where it
// lands. A format error is the cheapest half of that class and needs no harness
// to catch — an unparsable config is a harness that will not start.
func TestEveryTargetWritesReadableFiles(t *testing.T) {
	for _, target := range installTargetNames() {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

			if _, err := installTarget(target, "/usr/local/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := guidanceResult(target, false); err != nil {
				t.Fatalf("guidance: %v", err)
			}

			wrote := 0
			err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				raw, rerr := os.ReadFile(path)
				if rerr != nil {
					t.Errorf("%s: unreadable: %v", rel(home, path), rerr)
					return nil
				}
				wrote++
				if len(raw) == 0 {
					t.Errorf("%s: empty file", rel(home, path))
					return nil
				}
				switch {
				case strings.HasSuffix(path, ".json"):
					var v any
					if jerr := json.Unmarshal(raw, &v); jerr != nil {
						t.Errorf("%s: invalid JSON: %v", rel(home, path), jerr)
					}
				case strings.HasSuffix(path, "SKILL.md"):
					// The frontmatter is the whole contract: a skill without a
					// name and a description is a file no harness will load.
					if !strings.HasPrefix(string(raw), "---") {
						t.Errorf("%s: skill has no frontmatter", rel(home, path))
						break
					}
					head := strings.SplitN(string(raw), "---", 3)
					if len(head) < 3 {
						t.Errorf("%s: unterminated frontmatter", rel(home, path))
						break
					}
					for _, key := range []string{"name:", "description:"} {
						if !strings.Contains(head[1], key) {
							t.Errorf("%s: frontmatter has no %s", rel(home, path), key)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if wrote == 0 && target != "statusline" {
				t.Errorf("%s wrote nothing at all", target)
			}
		})
	}
}

func rel(home, path string) string {
	if r, err := filepath.Rel(home, path); err == nil {
		return r
	}
	return path
}
