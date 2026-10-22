package sources

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// hostileDir is what somebody else's machine looks like: a space, a percent
// octet that is not an escape, parentheses, a dot, and a non-ASCII name. Every
// parser fixture is read from under one.
//
// This is the class the Copilot Chat reader was wrong about — its resource
// paths were never percent-decoded, which nobody here could see because every
// path on this laptop is ASCII with no spaces, and a contributor with
// `C:\Users\me\my app` hit it immediately (#3498, #3505).
const hostileDir = "проект (v2.1) %20 name"

// Every registry fixture parses the same under a path nobody on this machine
// would have written.
func TestEveryRegistryFixtureParsesUnderAHostilePath(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	hermeticSourcesEnv(t)
	registry := readFormatRegistry(t, filepath.Join(root, "docs", "registry", "registry.json"))
	for _, entry := range registry.Harnesses {

		t.Run(entry.ID, func(t *testing.T) {
			// The whole fixture tree moves, not the one file: several parsers
			// read a sibling — kimi's state.json, goose's session header — and
			// copying the file alone would test the copy rather than the path.
			work := filepath.Join(t.TempDir(), hostileDir)
			copyTree(t, filepath.Join(root, "fixtures"), filepath.Join(work, "fixtures"))
			for _, fixture := range entry.FixturePaths {
				copied := filepath.Join(work, filepath.FromSlash(fixture))
				if _, err := os.Stat(copied); err != nil {
					t.Fatalf("fixture did not survive the copy: %v", err)
				}
				sessions := parseRegistryFixtureIn(t, entry.ID, copied, work)
				validateRegistrySessions(t, entry.ID, sessions)
			}
		})
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p) //nolint:gosec // the test copies its own fixture tree
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
