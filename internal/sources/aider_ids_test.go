package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A session's id is its own, not its place in the file: the same launch keeps
// its id whether or not the file still holds the launches before it, and two
// launches in one second still get two ids (#4332).
func TestAiderSessionIDDoesNotDependOnItsPlaceInTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	const later = "# aider chat started at 2026-10-01 09:00:00\n\n#### rename the config loader\n\nRenamed it.\n"
	parse := func(doc string) []string {
		t.Helper()
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseAiderFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		return ids
	}
	both := parse("# aider chat started at 2026-09-30 10:00:00\n\n#### add jitter\n\nAdded.\n\n" + later)
	alone := parse(later)
	if len(both) != 2 || len(alone) != 1 || both[1] != alone[0] {
		t.Fatalf("the 09:00 launch is %v beside an earlier one and %v alone", both, alone)
	}
	if both[0] == alone[0] {
		t.Fatalf("a new file's first launch took the deleted file's first id %q", both[0])
	}
	twin := parse(later + "\n" + later)
	if len(twin) != 2 || twin[0] == twin[1] {
		t.Fatalf("two launches in one second share an id: %v", twin)
	}
}
