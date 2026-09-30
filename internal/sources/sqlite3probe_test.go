package sources

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubSQLite3 puts a shell script named sqlite3 alone on PATH and returns its
// path. The probe cache is reset on both sides so neighbouring tests see the
// real binary again.
func stubSQLite3(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stubs")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "sqlite3")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ResetSQLite3Probe()
	t.Cleanup(ResetSQLite3Probe)
	return p
}

// withOpencodeStore gives the test a home holding an opencode database file,
// so SkipReason has a store to explain.
func withOpencodeStore(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_OPENCODE_DB", "")
	db := OpencodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A sqlite3 on PATH that does not answer read every store as empty with exit
// 0, which deja could not tell from an empty database.
func TestSQLite3ProbeCatchesABinaryThatDoesNotAnswer(t *testing.T) {
	real, realErr := exec.LookPath("sqlite3")
	cases := []struct {
		name, body, want string
		needsReal        bool
	}{
		{"first-arg wrapper", `exec ` + real + ` "$1"`, "did not answer a probe query (no output)", true},
		{"silent stub", "exit 0", "did not answer a probe query (no output)", false},
		{"garbage", "echo hello", `answered a probe query with "hello"`, false},
		{"failing", "echo 'Error: no such function: json_object' >&2; exit 1", "failed a probe query (exit status 1: Error: no such function: json_object)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.needsReal && realErr != nil {
				t.Skip("no real sqlite3 to wrap")
			}
			withOpencodeStore(t)
			stub := stubSQLite3(t, c.body)
			want := "sqlite3 at " + stub + " " + c.want
			if got := SQLite3Problem(); got != want {
				t.Fatalf("problem = %q, want %q", got, want)
			}
			if SQLite3Available() || !SQLite3Broken() {
				t.Error("a broken sqlite3 counts as available")
			}
			if got := SkipReason("opencode"); got != want {
				t.Errorf("opencode skip reason = %q, want %q", got, want)
			}
			if got := SkipReason("claude"); got != "" {
				t.Errorf("claude skip reason = %q, want none", got)
			}
		})
	}
}

func TestSQLite3ProbeAbsentAndWorking(t *testing.T) {
	withOpencodeStore(t)
	real, err := exec.LookPath("sqlite3")

	t.Setenv("PATH", t.TempDir())
	ResetSQLite3Probe()
	t.Cleanup(ResetSQLite3Probe)
	if got := SQLite3Problem(); got != SQLite3NotFound {
		t.Errorf("absent: problem = %q", got)
	}
	if SQLite3Broken() {
		t.Error("absent reported as broken")
	}
	if got := SkipReason("opencode"); got != SQLite3NotFound {
		t.Errorf("absent: skip reason = %q", got)
	}

	if err != nil {
		t.Skip("no real sqlite3")
	}
	t.Setenv("PATH", filepath.Dir(real))
	if got := SQLite3Problem(); got != "" {
		t.Errorf("real sqlite3: problem = %q", got)
	}
	if got := SkipReason("opencode"); got != "" {
		t.Errorf("real sqlite3: skip reason = %q", got)
	}
}

// One probe per binary: a second call must not start another process.
func TestSQLite3ProbeRunsOncePerBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stubs")
	}
	count := filepath.Join(t.TempDir(), "count")
	stubSQLite3(t, "echo x >> "+count)
	for range 3 {
		SQLite3Available()
		SkipReason("opencode")
	}
	b, _ := os.ReadFile(count)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Errorf("probed %d times, want 1", n)
	}
}

func TestZedSkipReasonCarriesTheSQLite3Problem(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ZED_ROOT", root)
	if err := os.MkdirAll(filepath.Dir(ZedDB()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ZedDB(), []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := stubSQLite3(t, "exit 0")
	got := SkipReason("zed")
	if !strings.HasPrefix(got, "sqlite3 at "+stub+" did not answer") {
		t.Errorf("zed skip reason = %q", got)
	}
}
