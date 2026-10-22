//go:build unix

package sources

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// hermesStub puts a sqlite3 on PATH that answers the probe and serves the
// Hermes message read the way DEJA_TEST_SQLITE says: slow drips a row every
// 0.1s, quiet prints only `.progress` lines for a while and then the rows,
// stall prints one row and stops, fail exits 1.
func hermesStub(t *testing.T, mode string) string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*"json_object('deja',1)"*) echo '{"deja":1}'; exit 0;;
*"from messages"*"order by session_id"*) ;;
*) exit 0;;
esac
row() { printf '{"session_id":"hs%s","role":"user","content":"slowneedle turn %s","timestamp":%s}\n' "$1" "$1" "$((1790000000+$1))"; }
case "$DEJA_TEST_SQLITE" in
fail) echo "Error: disk I/O error" >&2; exit 1;;
stall) row 1; exec sleep 30;;
orphan) row 1; sleep 30;;
silent) exec sleep 30;;
slow) for i in 1 2 3 4 5 6 7 8 9 10 11 12; do row $i; sleep 0.1; done;;
quiet) for i in 1 2 3 4 5 6 7 8 9 10 11 12; do echo "Progress $i"; sleep 0.1; done; for i in 1 2 3; do row $i; done;;
*) for i in 1 2 3; do row $i; done;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEJA_TEST_SQLITE", mode)
	ResetSQLite3Probe()
	t.Cleanup(ResetSQLite3Probe)
	db := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_HERMES_DB", db)
	t.Setenv("DEJA_HERMES_PG_DSN", "")
	DiagSnapshot()
	t.Cleanup(func() { DiagSnapshot() })
	return db
}

// A store read that is slow but keeps moving is read whole. The budget was a
// wall clock on the sqlite3 child, and a Hermes store still reading on a slow
// disk was cut at it and left out of the index (#5517).
func TestASlowSQLiteReadThatKeepsMovingIsNotCut(t *testing.T) {
	for _, mode := range []string{"slow", "quiet"} {
		t.Run(mode, func(t *testing.T) {
			db := hermesStub(t, mode)
			t.Setenv("DEJA_STORE_TIMEOUT", "500ms")
			started := time.Now()
			ss := LoadHermes()
			want := 12
			if mode == "quiet" {
				want = 3
			}
			if len(ss) != want {
				t.Fatalf("read %d sessions in %s, want %d", len(ss), time.Since(started).Round(time.Millisecond), want)
			}
			if time.Since(started) < 500*time.Millisecond {
				t.Fatal("the read was not slower than the budget, so it proves nothing")
			}
			if msg, ok := DiagFailedPaths()[db]; ok {
				t.Errorf("a read that kept moving was recorded as unread: %s", msg)
			}
		})
	}
}

// A read that stops moving is given up within the budget and the store is
// named as unread, not dropped in silence.
func TestAStalledSQLiteReadIsGivenUpAndNamed(t *testing.T) {
	db := hermesStub(t, "stall")
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	done := make(chan int, 1)
	go func() { done <- len(LoadHermes()) }()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("a store given up handed back %d sessions", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read with no progress held the store past its budget")
	}
	msg, ok := DiagFailedPaths()[db]
	if !ok {
		t.Fatalf("the store was not recorded as unread: %v", DiagFailedPaths())
	}
	if !strings.Contains(msg, "no progress for 300ms") || !strings.Contains(msg, "DEJA_STORE_TIMEOUT=0 waits") {
		t.Errorf("unread reason = %q", msg)
	}
	if idle, ok := DiagStalledPaths()[db]; !ok || idle != 300*time.Millisecond {
		t.Errorf("stalled paths = %v", DiagStalledPaths())
	}

	// With the bound off it waits, as the message says.
	t.Setenv("DEJA_STORE_TIMEOUT", "0")
	t.Setenv("DEJA_TEST_SQLITE", "slow")
	DiagSnapshot()
	if n := len(LoadHermes()); n != 12 {
		t.Errorf("with DEJA_STORE_TIMEOUT=0 read %d sessions, want 12", n)
	}
}

// A read given up before its first row is a stall, not a changed schema:
// that is the slow-disk case, where nothing has come back yet.
func TestAStallBeforeTheFirstRowIsNotASchemaChange(t *testing.T) {
	db := hermesStub(t, "silent")
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	LoadHermes()
	msg := DiagFailedPaths()[db]
	if !strings.Contains(msg, "no progress for 300ms") || strings.Contains(msg, "schema") {
		t.Errorf("unread reason = %q", msg)
	}
}

// A child the kill does not take down with its pipes — here a grandchild
// holding them, on a real disk a read stuck in the kernel — does not hold the
// run either.
func TestAGivenUpReadDoesNotWaitOnWhatHoldsItsPipes(t *testing.T) {
	db := hermesStub(t, "orphan")
	t.Setenv("DEJA_STORE_TIMEOUT", "300ms")
	saved := sqliteReapWait
	sqliteReapWait = 200 * time.Millisecond
	t.Cleanup(func() { sqliteReapWait = saved })
	started := time.Now()
	LoadHermes()
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("the run waited %s on a read it gave up", took.Round(time.Second))
	}
	if _, ok := DiagStalledPaths()[db]; !ok {
		t.Errorf("not recorded as stalled: %v", DiagFailedPaths())
	}
}

// A store whose read fails is recorded as unread. LoadHermes dropped the
// error, so a full build left Hermes out and exited 0 with nothing said.
func TestAFailedStoreReadIsRecorded(t *testing.T) {
	db := hermesStub(t, "fail")
	if n := len(LoadHermes()); n != 0 {
		t.Fatalf("a failed read handed back %d sessions", n)
	}
	if _, ok := DiagFailedPaths()[db]; !ok {
		t.Fatalf("the failed store was not recorded: %v", DiagFailedPaths())
	}
	if _, ok := DiagStalledPaths()[db]; ok {
		t.Error("a failed read was reported as stalled")
	}
	// The control: the same store read cleanly records nothing.
	t.Setenv("DEJA_TEST_SQLITE", "ok")
	DiagSnapshot()
	if n := len(LoadHermes()); n != 3 {
		t.Fatalf("read %d sessions, want 3", n)
	}
	if len(DiagFailedPaths()) != 0 {
		t.Errorf("a clean read recorded %v", DiagFailedPaths())
	}
}

// A sqlite3 that is broken is named by SkipReason on every pass. Its stores
// are not also counted as unread, or every pass would exit 3 over a tool.
func TestABrokenSQLite3IsNotCountedAsAnUnreadStore(t *testing.T) {
	db := hermesStub(t, "fail")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte("#!/bin/sh\necho 'Error: no such function: json_object' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ResetSQLite3Probe()
	LoadHermes()
	if msg, ok := DiagFailedPaths()[db]; ok {
		t.Errorf("a broken sqlite3 counted the store as unread: %s", msg)
	}
	if SkipReason("hermes") == "" {
		t.Error("nothing names the broken sqlite3")
	}
}

// The other database loaders dropped their error the same way.
func TestDatabaseLoadersRecordAFailedRead(t *testing.T) {
	hermesStub(t, "fail")
	// Every query fails, whatever it asks.
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *\"json_object('deja',1)\"*) echo '{\"deja\":1}'; exit 0;; esac\necho 'Error: disk I/O error' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ResetSQLite3Probe()
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := func(env string) string {
		p := filepath.Join(t.TempDir(), "store.db")
		if err := os.WriteFile(p, []byte("SQLite format 3\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(env, p)
		return p
	}
	for _, c := range []struct {
		name, env string
		load      func() []model.Session
	}{
		{"kilo", "DEJA_KILO_DB", LoadKilo},
		{"zcode", "DEJA_ZCODE_DB", LoadZCode},
		{"kiro", "DEJA_KIRO_DB", LoadKiro},
		{"zed", "DEJA_ZED_DB", LoadZed},
		{"grok", "DEJA_GROK_DB", LoadGrokDB},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := store(c.env)
			DiagSnapshot()
			_ = c.load()
			if _, ok := DiagFailedPaths()[p]; !ok {
				t.Errorf("%s: a failed read was not recorded: %v", c.name, DiagFailedPaths())
			}
		})
	}
}

// The progress lines are taken out and nothing else is: a row that starts
// like one, a line too long for the buffer, a last line with no newline.
func TestProgressFilterTakesOutOnlyProgressLines(t *testing.T) {
	long := strings.Repeat("x", 100<<10)
	in := "Progress 1\n{\"a\":1}\nProgress 22\r\nProgress of the day\nProgress \n" + long + "Progress 3\n" + "\nProgress 4\ntail"
	want := "{\"a\":1}\nProgress of the day\nProgress \n" + long + "Progress 3\n" + "\ntail"
	moved := 0
	f := &progressFilter{r: bufio.NewReaderSize(strings.NewReader(in), 64<<10), moved: func() { moved++ }}
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("filtered output differs:\n got %q\nwant %q", clip(string(got)), clip(want))
	}
	if moved == 0 {
		t.Error("no movement reported")
	}
	// The end of a read given up says so, not what the closed pipe said.
	stall := &StalledRead{Path: "x", Idle: time.Second}
	f = &progressFilter{r: bufio.NewReader(strings.NewReader("{}\n")), ended: func(error) error { return stall }}
	if _, err := io.ReadAll(f); !errors.Is(err, stall) {
		t.Errorf("end of a given-up read = %v", err)
	}
}
