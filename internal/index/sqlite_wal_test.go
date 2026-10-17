package index

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A SQLite store in WAL mode takes its writes in `<db>-wal` and touches the
// database file only at a checkpoint. The walk fingerprinted the file alone,
// so everything still in the log was never read (Hermes checkpoints every 50
// writes of one process, and a short session never gets there).
func TestTheWALBesideAStoreIsPartOfItsFileState(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	cases := []struct {
		name    string
		before  string // WAL contents at the first walk; "" means no WAL
		after   string // WAL contents at the second walk; "" means no WAL
		moved   time.Duration
		changed bool
	}{
		{name: "nothing moved", changed: false},
		{name: "a WAL untouched", before: "frames", after: "frames", changed: false},
		{name: "a WAL appears", after: "frames", changed: true},
		{name: "the WAL grows", before: "frames", after: "frames and more", changed: true},
		{name: "the WAL is rewritten in place", before: "frames", after: "FRAMES", moved: time.Minute, changed: true},
		{name: "a checkpoint removes the WAL", before: "frames", changed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			isolateStores(t, tmp)
			db := filepath.Join(tmp, "store", "state.db")
			writeAt(t, db, "SQLite format 3\x00", at)
			wal := db + "-wal"
			walk := func(body string, stamp time.Time) FileState {
				t.Helper()
				if body == "" {
					if err := os.Remove(wal); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
				} else {
					writeAt(t, wal, body, stamp)
				}
				fi, err := os.Lstat(db)
				if err != nil {
					t.Fatal(err)
				}
				return walkFileState(db, fi, nil)
			}
			first := walk(tc.before, at)
			second := walk(tc.after, at.Add(tc.moved))
			if got := !sameFile(first, second); got != tc.changed {
				t.Errorf("changed = %v, want %v (first %+v, second %+v)", got, tc.changed, first, second)
			}
		})
	}
}

// A transcript is not a SQLite store: a file beside it that happens to end in
// -wal is not its log.
func TestATranscriptIgnoresAFileNamedLikeAWAL(t *testing.T) {
	tmp := t.TempDir()
	isolateStores(t, tmp)
	p := filepath.Join(tmp, "store", "s.jsonl")
	at := time.Now().Add(-time.Hour)
	writeAt(t, p, "{}\n", at)
	writeAt(t, p+"-wal", "frames", at)
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fs := walkFileState(p, fi, nil); fs.WALSize != 0 || fs.WALMTime != 0 {
		t.Errorf("a transcript took the WAL beside it into its state: %+v", fs)
	}
}

// The repro: a Hermes session written while its process holds the store
// open lives only in the WAL. The next pass said "up to date" and the session
// was missing from recall until a checkpoint touched the database file.
func TestAHermesSessionStillInTheWALIsIndexed(t *testing.T) {
	tmp, home := hermesEnv(t)
	db := filepath.Join(home, "state.db")
	seedHermes(t, db, `pragma journal_mode=wal; insert into messages values (1,'s1','user','marker-kestrel-seven',1767268800);`)
	if _, err := os.Stat(db + "-wal"); err == nil {
		t.Fatalf("the WAL outlived the seeding connection, so this measures nothing")
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-kestrel-seven") != 1 {
		t.Fatalf("the store was not indexed at all, so this measures nothing")
	}
	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}

	// The agent's process: one connection, held open past its write, the way
	// a CLI session sits between turns.
	cmd := exec.Command("sqlite3", db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stdin.Close(); err != nil {
			t.Error(err)
		}
		_, _ = io.Copy(io.Discard, stdout)
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := io.WriteString(stdin, "insert into messages values (2,'s2','user','marker-osprey-three',1767272400);\nselect 'written';\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "written" {
		t.Fatalf("the writer did not finish its insert: %q %v", line, err)
	}
	after, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the insert reached the database file, so this measures nothing")
	}

	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if hermesHits(t, dir, "marker-osprey-three") != 1 {
		t.Errorf("a session held in the WAL was not indexed")
	}
	if hermesHits(t, dir, "marker-kestrel-seven") != 1 {
		t.Errorf("re-reading the store lost the session already indexed")
	}
}
