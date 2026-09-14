package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build against a path that holds a file used to delete the file: the swap
// parks whatever is at the index path as <dir>.old, renames the new index into
// place, and then removes the parked copy. Exit code 0, nothing printed, and a
// file somebody put there gone — DEJA_INDEX_DIR pointing at a database by
// mistake is enough to reach it.
func TestABuildRefusesAnIndexPathThatIsAFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index.db")
	const content = "notes somebody kept here\n"
	if err := os.WriteFile(dir, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Ensure(dir, "", true, nil)
	if err == nil {
		t.Fatal("a build against a file for an index path reported success")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the refusal does not name the path: %v", err)
	}
	b, rerr := os.ReadFile(dir)
	if rerr != nil {
		t.Fatalf("the file at the index path is gone: %v", rerr)
	}
	if string(b) != content {
		t.Errorf("the file at the index path was rewritten: %q", b)
	}
}
