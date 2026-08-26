package index

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A control byte in tracked text is invisible in an editor and invisible in a
// diff, so it survives review by being unreadable. One is in this repository:
// a comment in `cmd/deja/doctor_sync_json_test.go` said the bytes on the wire
// carry no raw escape and carried one (#1983). Another reached a comment on a
// branch during this work and was found only because a reviewer ran `od -c`
// over the diff, which is not a review anyone can be asked to repeat.
//
// Tab, newline and carriage return are the three a text file is made of. A file
// holding a NUL is taken as binary and skipped — today that is two PNGs and two
// GIFs and nothing else, no fixture among them, and the cost of the heuristic is
// that a text file which somehow contains a NUL is skipped with them.
func TestNoControlBytesInTrackedText(t *testing.T) {
	root := repoRoot(t)
	// Tracked files only, from git rather than from the filesystem: a walk
	// reads a developer's build output, their .venv, and the agent-session
	// directories the .gitignore expects in this checkout — all of them full of
	// captured terminal escapes. That failure lands only on the machines with
	// junk in the tree, never in CI, which is the worst way for a guard to be
	// wrong.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("no git checkout to ask: %v", err)
	}

	var found []string
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(root, string(name))
		b, err := os.ReadFile(path)
		if err != nil {
			// A tracked file that cannot be read is not this test's finding,
			// but it is not a pass either.
			if !os.IsNotExist(err) {
				t.Errorf("cannot read tracked file %s: %v", name, err)
			}
			continue
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue
		}
		for i, c := range b {
			if c == '\t' || c == '\n' || c == '\r' {
				continue
			}
			if c < 0x20 || c == 0x7f {
				line := 1 + bytes.Count(b[:i], []byte("\n"))
				found = append(found, fmt.Sprintf("  %s:%d: byte 0x%02x", name, line, c))
				break
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("control bytes in tracked text, which no editor and no diff will show:\n%s",
			strings.Join(found, "\n"))
	}
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
