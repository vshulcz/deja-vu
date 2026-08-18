package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The generation answers one question: do record offsets still mean what they
// meant. A timestamp alone cannot answer it — the incremental path deliberately
// carries the previous stamp forward, and two rebuilds inside one tick of a
// coarse clock share one, which is how a sidecar built before a `deja forget`
// read as current on Linux (#1355).
func TestGenerationChangesWhenRecordsMove(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "the vault rotation broke staging")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "b", "the kafka consumer keeps flapping")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	after, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("the generation is %q both before and after records grew", before)
	}
}

// And it stays put when nothing moved: re-reading an unchanged index must not
// invalidate a sidecar built for it.
func TestGenerationStaysWhenNothingMoved(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "a", "the vault rotation broke staging")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	first, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	second, err := Generation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("the generation moved from %q to %q with nothing changed", first, second)
	}
}
