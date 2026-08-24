package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Install writes skills into directories that belong to the user, and it used
// to replace one whenever its contents differed from what deja generates. That
// is right for a stale copy an older deja wrote and wrong for a file someone
// edited, and nothing on disk told the two apart: a person who tuned the
// wording of their skill lost it on the next install, silently.
//
// What deja wrote is recorded here, beside its own state rather than beside the
// skill. A marker file in a skills directory is a file the harness may try to
// parse, and the point of this is to stop leaving things in the user's
// directories that they did not ask for.
func skillMarksPath() string { return index.DefaultDir() + ".skills" }

func skillMarks() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(skillMarksPath())
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if sum, path, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			out[path] = sum
		}
	}
	return out
}

func rememberSkill(path string, content []byte) {
	marks := skillMarks()
	marks[path] = contentMark(content)
	paths := make([]string, 0, len(marks))
	for p := range marks {
		paths = append(paths, p)
	}
	// Sorted so rewriting the file is a no-op when nothing changed, rather than
	// a fresh permutation of the same lines.
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(marks[p])
		b.WriteByte(' ')
		b.WriteString(p)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(skillMarksPath(), []byte(b.String()), 0o600)
}

func contentMark(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// skillWasEdited reports whether the file on disk is something other than what
// deja last wrote there.
//
// A file with no mark is treated as deja's. That is the state every existing
// install is in, and calling all of them edited would refuse to update a skill
// on every machine that has one — the protection starts from the next write.
func skillWasEdited(path string, old []byte) bool {
	if len(old) == 0 {
		return false
	}
	mark, ok := skillMarks()[path]
	if !ok {
		return false
	}
	return mark != contentMark(old)
}

// skillIsMarked reports whether deja has a record of writing this file.
func skillIsMarked(path string) bool {
	_, ok := skillMarks()[path]
	return ok
}
