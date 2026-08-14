package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Homes with a space in them are ordinary on macOS ("/Users/John Smith") and
// Windows ("C:\\Program Files"). deja writes its hook as one command string, so
// any harness that splits that string on whitespace runs "/Users/John" with
// "Smith/bin/deja" as an argument. Whatever the answer per harness, the path
// must not be written bare into a place that needs it quoted.
func TestSpaceInTheBinaryPathIsQuotedWhereItMustBe(t *testing.T) {
	const exe = "/Users/John Smith/bin/deja"

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

			if _, err := installTarget(target, exe, false); err != nil {
				t.Fatalf("install: %v", err)
			}

			_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || strings.HasPrefix(path, filepath.Join(home, "index.db")) {
					return nil
				}
				b, rerr := os.ReadFile(path)
				if rerr != nil {
					return nil
				}
				body := string(b)
				if !strings.Contains(body, "John Smith") {
					return nil
				}
				rel, _ := filepath.Rel(home, path)
				// A shell-quoted or JSON-quoted path is fine. What is not is
				// the path sitting bare in a YAML scalar or a shell line,
				// where the space ends the word.
				for _, line := range strings.Split(body, "\n") {
					if !strings.Contains(line, "John Smith") {
						continue
					}
					trimmed := strings.TrimSpace(line)
					quoted := strings.Contains(line, `"`+exe) || strings.Contains(line, `'`+exe) ||
						strings.Contains(line, `"`+exe+` `) || strings.Contains(line, exe+`"`) ||
						strings.Contains(line, `John Smith`) && strings.Count(line, `"`) >= 2
					if !quoted {
						t.Errorf("%s has the path unquoted, so the space ends the word:\n  %s", rel, trimmed)
					}
				}
				return nil
			})
		})
	}
}
