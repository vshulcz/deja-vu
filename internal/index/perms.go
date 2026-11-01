package index

import (
	"os"
	"path/filepath"
	"strings"
)

// SharedGroup reports whether the index at path is configured for shared
// multi-user group access (#5759, #5772): either DEJA_SHARED_GROUP=1 is set
// in the environment, or the index directory (or its parent directory) carries
// the Unix setgid bit (os.ModeSetgid, chmod 2770).
func SharedGroup(path string) bool {
	if v := strings.TrimSpace(os.Getenv("DEJA_SHARED_GROUP")); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	if path == "" {
		return false
	}
	cur := filepath.Clean(path)
	for range 4 {
		for _, cand := range []string{
			cur,
			strings.TrimSuffix(cur, ".tmp"),
			strings.TrimSuffix(cur, ".part"),
			strings.TrimSuffix(cur, ".lock"),
		} {
			if cand == "" {
				continue
			}
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() && fi.Mode()&os.ModeSetgid != 0 {
				return true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return false
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
	shared := SharedGroup(path)
	perm := os.FileMode(0o600)
	if shared {
		perm = 0o660
	}
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	if shared {
		_ = f.Chmod(0o660)
	}
	return f, nil
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
// (including its sibling .lock file) that are missing group read+write
// permissions (0o060). Returns 0 when SharedGroup(dir) is false.
func UnsharedFiles(dir string) int {
	if !SharedGroup(dir) {
		return 0
	}
	bad := 0
	for _, p := range []string{dir, dir + ".lock", filepath.Join(dir, "buckets")} {
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
