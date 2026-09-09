package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Four bugs in one week were the same shape: an -auto target writing two files
// and reporting one (#3185, #3220, #3208, #3171). This is the general form —
// install each target on an empty home, list what appeared, and require the
// report to name it, by its own path or by a directory the report named whole
// (#3254).
func TestEveryAutoTargetNamesWhatItWrote(t *testing.T) {
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		t.Run(target, func(t *testing.T) {
			tmp := hermeticEnv(t)
			home := filepath.Join(tmp, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			before := absFilesUnder(t, home)
			out, err := captureRun(t, "install", target, "--no-index")
			if err != nil {
				t.Skipf("target refused here: %v", err)
			}
			for p := range absFilesUnder(t, home) {
				if before[p] || dejasOwnBookkeeping(p) {
					continue
				}
				if !reportNames(out, p) {
					t.Errorf("wrote %s and did not say so:\n%s", shortHome(p), out)
				}
			}
		})
	}
}

// dejasOwnBookkeeping is what an install writes for itself rather than for the
// harness: the wiring record, the index, the notes file, the warmup sentinel,
// and the snapshots, which have a line of their own.
func dejasOwnBookkeeping(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".bak"), strings.HasPrefix(base, ".deja"):
		return true
	case base == "wiring.json", base == "notes.jsonl":
		return true
	}
	return strings.Contains(path, string(filepath.Separator)+".cache"+string(filepath.Separator)) ||
		strings.Contains(path, "index.db")
}

// reportNames reports whether the install said it wrote this file: the path
// itself, or a directory holding it — named as a directory, not as the head of
// some other path. Both spellings, since the report shortens the home
// directory to ~.
func reportNames(out, path string) bool {
	for p := path; len(p) > 1; p = filepath.Dir(p) {
		if namedWhole(out, p) || namedWhole(out, shortHome(p)) {
			return true
		}
	}
	return false
}

// namedWhole reports whether the text names this path and stops there. Without
// the boundary, "~/.agents/skills" counts as named because it is the head of
// the deja-history skill's path, and the file beside it goes unmentioned —
// which is the shape #3254 is about.
func namedWhole(out, path string) bool {
	for i := 0; ; {
		j := strings.Index(out[i:], path)
		if j < 0 {
			return false
		}
		end := i + j + len(path)
		if end >= len(out) || (out[end] != '/' && out[end] != '\\') {
			return true
		}
		i = end
	}
}

func absFilesUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		out[p] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
