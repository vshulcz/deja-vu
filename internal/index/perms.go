package index

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// SharedGroup reports whether the index at path is configured for shared
// multi-user group access (#5759, #5772, #5807): either DEJA_SHARED_GROUP=1 is
// set in the environment, or the index directory (or its parent directory)
// carries the Unix setgid bit (os.ModeSetgid, chmod 2770).
func SharedGroup(path string) bool {
	return atomicfile.SharedGroup(path)
}

func sharedSiblingSidecars(dir string) []string {
	if dir == "" {
		return nil
	}
	return []string{
		dir + ".lock",
		dir + ".live",
		dir + ".hookseen",
		dir + ".dejavu",
		dir + ".forks",
		dir + ".warming",
		dir + ".usage.jsonl",
		dir + ".injections.jsonl",
		filepath.Join(filepath.Dir(dir), "compactions.json"),
	}
}

func chmodIndexDir(dir string) {
	if SharedGroup(dir) {
		_ = os.Chmod(dir, 0o770|os.ModeSetgid)
		return
	}
	_ = os.Chmod(dir, 0o700)
}

func mkdirIndexAll(path string) error {
	if !SharedGroup(path) {
		return os.MkdirAll(path, 0o700)
	}
	if err := os.MkdirAll(path, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o770|os.ModeSetgid)
	if parent := filepath.Dir(path); strings.HasSuffix(parent, ".tmp") || strings.HasSuffix(parent, ".part") {
		_ = os.Chmod(parent, 0o770|os.ModeSetgid)
	}
	return nil
}

func openIndexFileWrite(path string, flag int) (*os.File, error) {
	return atomicfile.OpenSharedFile(path, flag, 0o600)
}

func writeIndexFile(path string, data []byte) error {
	shared := SharedGroup(path)
	perm := os.FileMode(0o600)
	if shared {
		perm = 0o660
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	if shared {
		_ = os.Chmod(path, 0o660)
	}
	return nil
}

// UnsharedFiles returns the number of files or directories in a shared index
// (including its sibling .lock and hook/usage sidecar files) that are missing
// group read+write permissions (0o060). Returns 0 when SharedGroup(dir) is false.
func UnsharedFiles(dir string) int {
	if !SharedGroup(dir) {
		return 0
	}
	bad := 0
	paths := append([]string{dir, filepath.Join(dir, "buckets")}, sharedSiblingSidecars(dir)...)
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o060 != 0o060 {
			bad++
		}
	}
	for _, sub := range []string{dir, filepath.Join(dir, "buckets")} {
		entries, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if fi, sErr := e.Info(); sErr == nil && fi.Mode().Perm()&0o060 != 0o060 {
				bad++
			}
		}
	}
	return bad
}

func ensureSharedIndexPerms(dir string) {
	if !SharedGroup(dir) {
		return
	}
	for _, p := range sharedSiblingSidecars(dir) {
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o660)
		}
	}
	for _, d := range []string{dir, filepath.Join(dir, "buckets")} {
		_ = os.Chmod(d, 0o770|os.ModeSetgid)
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			_ = os.Chmod(filepath.Join(d, e.Name()), 0o660)
		}
	}
}
