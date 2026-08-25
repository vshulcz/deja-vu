package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// oneSnapshot writes a snapshots file holding exactly the given line.
func oneSnapshot(t *testing.T, line string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := SnapshotPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The usage log asks one question of a line since #1917 — a stamp and a kind.
// The snapshots file beside it asked a different one, so `deja log --last`
// could print a heading with no kind, or a digest dated the year 1 (#1946).
// Both are things deja cannot write: every writer passes a kind, and the stamp
// is time.Now().
func TestASnapshotNeedsAStampAKindAndADigest(t *testing.T) {
	for _, c := range []struct {
		name, line string
		kept       bool
	}{
		{"whole", `{"t":"2026-08-20T10:00:00Z","kind":"hook","bytes":100,"sessions":1,"digest":"earlier work"}`, true},
		{"unknown kind", `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100,"digest":"earlier work"}`, true},
		{"no digest", `{"t":"2026-08-20T10:00:00Z","kind":"hook","bytes":100,"sessions":1}`, false},
		{"no stamp", `{"kind":"hook","bytes":100,"sessions":1,"digest":"earlier work"}`, false},
		{"no kind", `{"t":"2026-08-20T10:00:00Z","bytes":100,"sessions":1,"digest":"earlier work"}`, false},
		{"blank line", ``, false},
	} {
		got := Snapshots(oneSnapshot(t, c.line), 0)
		want := 0
		if c.kept {
			want = 1
		}
		if len(got) != want {
			t.Errorf("%s: kept %d, want %d", c.name, len(got), want)
		}
	}
}

// And what deja writes is still read back whole — the rule must not tighten
// into "a kind I know".
func TestAWrittenSnapshotSurvivesTheRule(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	SnapshotPolicy(dir, KindHook, "a digest of earlier work", 2, "local-only")

	got := Snapshots(dir, 0)
	if len(got) != 1 {
		t.Fatalf("wrote one snapshot, read %d", len(got))
	}
	if got[0].Kind != KindHook || got[0].Digest == "" || got[0].Time.IsZero() {
		t.Errorf("the snapshot came back missing something: %#v", got[0])
	}
}
