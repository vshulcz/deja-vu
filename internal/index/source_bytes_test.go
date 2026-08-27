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
	names, err := filesUnderReview(root)
	if err != nil {
		t.Skipf("no git checkout to ask: %v", err)
	}

	var found []string
	for _, name := range names {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(root, name)
		b, err := os.ReadFile(path)
		if err != nil {
			// A file that cannot be read is not this test's finding, but it
			// is not a pass either.
			if !os.IsNotExist(err) {
				t.Errorf("cannot read %s: %v", name, err)
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
		t.Errorf("control bytes in the files a reviewer would see, which no editor and no diff will show:\n%s",
			strings.Join(found, "\n"))
	}
}

// filesUnderReview is what git would show a reviewer: everything tracked, plus
// the files that are untracked and not ignored.
//
// From git rather than from the filesystem, for the reason the test's comment
// gives: a walk reads a developer's build output, their .venv and the
// agent-session directories this checkout's .gitignore expects, all of them
// full of captured terminal escapes, and that failure lands only on the
// machines with junk in the tree.
//
// `--others --exclude-standard` is the other half of the same list. Without it
// the guard is silent exactly while a new file is being written and speaks only
// after `git add`, in CI, on someone else's machine — which is how a literal
// escape byte reached #2091.
func filesUnderReview(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		names = append(names, string(name))
	}
	return names, nil
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

// The list is what a reviewer would see, which includes a file that has not
// been added yet — the guard used to be silent exactly while one was being
// written (#2098).
func TestTheFileListHoldsANewFileBeforeItIsAdded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	// Without its own config the scratch repo inherits the developer's
	// core.excludesFile, and what this test asserts about ignoring is then
	// whatever that file happens to say.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "junk/\n")
	write("committed.go", "package p\n")
	git("add", ".gitignore", "committed.go")
	git("commit", "-q", "-m", "seed")
	write("brand_new.go", "package p\n")
	write("junk/captured.log", "\x1b[31m\n")

	names, err := filesUnderReview(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["committed.go"] {
		t.Error("a committed file is missing from the list")
	}
	if !got["brand_new.go"] {
		t.Error("a file that has not been added yet is missing from the list")
	}
	if got["junk/captured.log"] {
		t.Error("an ignored file is in the list, which is what reading the filesystem would do")
	}
}
