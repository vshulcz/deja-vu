package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sharedPair writes a and b under one project and returns their paths.
func sharedPair(t *testing.T, bodyA, bodyB string) (string, string) {
	t.Helper()
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a, b := filepath.Join(c, "p", "a.jsonl"), filepath.Join(c, "p", "b.jsonl")
	write(t, a, bodyA)
	write(t, b, bodyB)
	return a, b
}

// keptMatchesRebuild compares the row against a rebuild of a copy of dir, which
// carries the deleted files' records as a rebuild of dir would.
func keptMatchesRebuild(t *testing.T, dir, id string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuilt")
	if err := os.CopyFS(fresh, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	view := func(dir string) string {
		return sortedLines(sessionView(t, dir, "claude", id) + rowView(t, dir, "claude", id) + copiesView(t, dir, "claude", id))
	}
	if got, want := view(dir), view(fresh); got != want {
		t.Errorf("claude:%s after incremental passes:\n%s\nafter a rebuild:\n%s", id, got, want)
	}
}

func holdsTurn(t *testing.T, dir, id, text string) bool {
	t.Helper()
	return strings.Contains(sessionView(t, dir, "claude", id), "|"+text+"\n")
}

// A deleted transcript that shares its session with another, and does not own
// the row, went with its records: the turns only it held left the index.
func TestADeletedCopyKeepsTheTurnsOnlyItHeld(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if !holdsTurn(t, dir, "s-x", "bravo webhook") {
		t.Errorf("the deleted copy's own turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	keptMatchesRebuild(t, dir, "s-x")
}

// A deleted transcript's turns stay with the session when the copy on disk
// that shared them is rewritten without them: under that copy, through a
// rebuild or from the start, they went with the rewrite.
func TestARewrittenCopyLeavesADeletedTranscriptsTurns(t *testing.T) {
	body := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile") + claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	for _, tc := range []struct {
		name    string
		deleted int // 0 deletes a, the owner; 1 deletes b
		rebuild bool
	}{
		{"owner deleted, then a rebuild", 0, true},
		{"copy deleted", 1, false},
		{"copy deleted, then a rebuild", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			a, b := sharedPair(t, body, body)
			gone, live := a, b
			if tc.deleted == 1 {
				gone, live = b, a
			}
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			if err := os.Remove(gone); err != nil {
				t.Fatal(err)
			}
			indexPass(t, dir)
			if tc.rebuild {
				if err := Ensure(dir, "", true, nil); err != nil {
					t.Fatal(err)
				}
			}
			write(t, live, claudeLine("s-y", "2026-01-02T05:00:00Z", "cache queue"))
			bumpMTime(t, live)
			indexPass(t, dir)
			for _, turn := range []string{"alpha lockfile", "bravo webhook"} {
				if !holdsTurn(t, dir, "s-x", turn) {
					t.Errorf("%q of the deleted transcript left the index:\n%s", turn, sessionView(t, dir, "claude", "s-x"))
				}
			}
			keptMatchesRebuild(t, dir, "s-x")
		})
	}
}

// A transcript that now holds another session gives up the row of the one it
// held, as a rebuild does. Beside a deleted copy's records it kept the row and
// the title of a turn no longer in the session.
func TestAFileThatLeftTheSessionGivesUpItsRow(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a, b, cc := filepath.Join(c, "p", "a.jsonl"), filepath.Join(c, "p", "b.jsonl"), filepath.Join(c, "p", "c.jsonl")
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	write(t, b, t0+claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	write(t, a, claudeLine("s-x", "2026-01-02T02:59:00Z", "queue first")+t0)
	write(t, cc, t0+claudeLine("s-x", "2026-01-02T03:02:00Z", "cache retry"))
	indexPass(t, dir)
	write(t, a, claudeLine("s-y", "2026-01-02T05:00:00Z", "delta other"))
	bumpMTime(t, a)
	appendTo(t, cc, claudeLine("s-x", "2026-01-02T03:03:00Z", "retry again"))
	indexPass(t, dir)
	keptMatchesRebuild(t, dir, "s-x")
}

// A renamed transcript that shares its session is not followed by name alone:
// the row goes to the smallest path, as a rebuild gives it.
func TestARenamedCopyOwnsTheRowARebuildGivesIt(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	a, _ := sharedPair(t, claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile"),
		claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Rename(a, filepath.Join(filepath.Dir(a), "c.jsonl")); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	keptMatchesRebuild(t, dir, "s-x")
}

// A new file under a deleted transcript's id that holds only its last turn is
// a copy, not the transcript moved: the turns only the deleted one held stay.
func TestADeletedTranscriptIsNotMovedToACopyOfItsLastTurn(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	c := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(c, "p", "a.jsonl")
	t1 := claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	write(t, a, claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")+t1)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c, "p", "b.jsonl"), t1)
	indexPass(t, dir)
	if !holdsTurn(t, dir, "s-x", "alpha lockfile") {
		t.Errorf("the deleted transcript's first turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}

	// The control: a new file holding every turn, grown on the way, is the
	// transcript moved, and the old path goes.
	q := filepath.Join(c, "q", "a.jsonl")
	body := claudeLine("s-z", "2026-01-02T04:00:00Z", "cache queue") + claudeLine("s-z", "2026-01-02T04:01:00Z", "delta retry")
	write(t, q, body)
	indexPass(t, dir)
	if err := os.Remove(q); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c, "q", "moved.jsonl"), body+claudeLine("s-z", "2026-01-02T04:02:00Z", "queue webhook"))
	indexPass(t, dir)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[q]; ok {
		t.Errorf("a moved transcript kept its old path in the index")
	}
}

// Shared turns a rebuild files under the deleted transcript keep their place
// in the session: skipped in the live copy and written after it, a session
// read back its first turns last.
func TestARebuildKeepsTheOrderOfTurnsADeletedCopyShared(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile") + claudeLine("s-x", "2026-01-02T03:01:00Z", "bravo webhook")
	a, b := sharedPair(t, t0, t0+claudeLine("s-x", "2026-01-02T03:02:00Z", "cache queue"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	appendTo(t, b, claudeLine("s-x", "2026-01-02T03:03:00Z", "delta retry"))
	indexPass(t, dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := FindByIdentity(dir, "claude", "s-x")
	if err != nil || !ok {
		t.Fatalf("s-x not found: %v", err)
	}
	var got []string
	for _, m := range s.Messages {
		got = append(got, m.Text)
	}
	if want := "alpha lockfile|bravo webhook|cache queue|delta retry"; strings.Join(got, "|") != want {
		t.Errorf("turns after a rebuild: %v, want %s", got, want)
	}
}

// A turn written after the deleted copy was last written was never in it, so
// a rewind of the live copy takes it out of the index, deleted copy or not.
func TestARewoundTurnIsNotKeptForADeletedCopy(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	a, b := sharedPair(t, t0, t0)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	write(t, a, t0+claudeLine("s-x", later, "wrong secretword"))
	bumpMTime(t, a)
	indexPass(t, dir)
	write(t, a, t0+claudeLine("s-x", later, "right turn"))
	bumpMTime(t, a)
	indexPass(t, dir)
	if holdsTurn(t, dir, "s-x", "wrong secretword") {
		t.Errorf("a rewound turn stayed under the deleted copy:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	if !holdsTurn(t, dir, "s-x", "alpha lockfile") {
		t.Errorf("the shared turn left the index:\n%s", sessionView(t, dir, "claude", "s-x"))
	}
	keptMatchesRebuild(t, dir, "s-x")
}

// A forgotten session's deleted copies go with it, from the index forget
// writes and from a rebuild: kept, they counted as still searchable.
func TestAForgottenSessionTakesItsDeletedCopiesWithIt(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	// The tombstone forget writes stays with this test.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t0 := claudeLine("s-x", "2026-01-02T03:00:00Z", "alpha lockfile")
	_, b := sharedPair(t, t0, t0)
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if _, err := Forget(dir, ForgetOptions{Session: "s-x"}); err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		if err := Ensure(dir, "", rebuild, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := m.Files[b]; ok && f.Kept {
			t.Errorf("rebuild=%v: the forgotten session's deleted copy is still kept", rebuild)
		}
	}
}
