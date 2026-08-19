package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Watermarks are per peer, and a pull left the remote's export unnamed — so it
// shared one watermark with every `deja sync export` run by hand there. Take a
// backup on the server, then pull from it, and the pull receives almost
// nothing: the backup already settled those records. Measured on a real index,
// the second unnamed export sent 2 records where the first sent 246822.
func TestSyncPullNamesThisMachineToTheRemote(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var exportCmd string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[1], "sync export"):
			exportCmd = args[1]
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatal(err)
	}
	// The whole remote command is quoted again for `sh -lc`, so the name
	// arrives escaped; what matters is that the flag and the name are in it.
	if !strings.Contains(exportCmd, "--peer") || !strings.Contains(exportCmd, "laptop") {
		t.Errorf("the pull did not tell the remote who it is: %s", exportCmd)
	}
}

// A machine name is not guaranteed to be a bare word, and it is spliced into a
// command the remote shell runs.
func TestSyncPullQuotesAnAwkwardMachineName(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "lap; rm -rf ~")
	old := sshRunner
	defer func() { sshRunner = old }()
	var exportCmd string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[1], "sync export"):
			exportCmd = args[1]
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatal(err)
	}
	// Unwrap the one level `sh -lc '…'` added: what is left is the command the
	// remote shell sees, and the semicolon in the name must still be inside a
	// quoted word there.
	inner := strings.ReplaceAll(exportCmd, `'"'"'`, "'")
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "sh -lc '"), "'")
	if !strings.Contains(inner, `'lap; rm -rf ~'`) {
		t.Errorf("the machine name reached the remote shell unquoted: %s", inner)
	}
}

// The flag is refused by a deja too old to know it, which is what stops a typo
// from exporting nothing. Both machines are not upgraded at the same moment,
// so a pull falls back to the shared watermark rather than failing.
func TestSyncPullFallsBackWhenTheRemoteIsOlder(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var commands []string
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[1], "sync export"):
			commands = append(commands, args[1])
			if strings.Contains(args[1], "--peer") {
				return `deja: sync export: unknown flag "--peer" — only --full is accepted`, errors.New("exit status 1")
			}
			return "deja: exported 0 records", nil
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err != nil {
		t.Fatalf("a pull from an older remote failed instead of falling back: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("ran %d remote exports, want the named one and the fallback: %v", len(commands), commands)
	}
	if strings.Contains(commands[1], "--peer") {
		t.Errorf("the fallback still carried the flag: %s", commands[1])
	}
}

// A remote error that is not about the flag must not be retried: running the
// export twice would advance the remote's watermark a second time.
func TestSyncPullDoesNotRetryOtherFailures(t *testing.T) {
	setupLocalIndex(t)
	t.Setenv("DEJA_MACHINE", "laptop")
	old := sshRunner
	defer func() { sshRunner = old }()
	var calls int
	sshRunner = func(name string, args ...string) (string, error) {
		switch {
		case name == "ssh" && args[1] == "mktemp -d":
			return "/tmp/remote-out", nil
		case name == "ssh" && strings.Contains(args[1], "sync export"):
			calls++
			return "deja: no sessions indexed yet", errors.New("exit status 1")
		}
		return "", nil
	}
	if err := runSyncSSH(os.Getenv("DEJA_INDEX_DIR"), []string{"mini", "--pull"}); err == nil {
		t.Fatal("a failing remote export was reported as success")
	}
	if calls != 1 {
		t.Errorf("the remote export ran %d times, want once", calls)
	}
}

// The flag on this side: a named peer keeps its own watermark, so a second
// export for a different name still carries the whole history.
func TestExportPeerKeepsItsOwnWatermark(t *testing.T) {
	tmp := setupLocalIndex(t)
	dir := os.Getenv("DEJA_INDEX_DIR")

	first := filepath.Join(tmp, "backup")
	if err := runSync(dir, []string{"export", first}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, first); n == 0 {
		t.Fatal("the first export sent nothing")
	}

	// Same records, different peer: the backup above must not have settled
	// them for this one.
	named := filepath.Join(tmp, "toMini")
	if err := runSync(dir, []string{"export", named, "--peer", "mini"}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, named); n == 0 {
		t.Error("a hand-taken backup starved the export for a named peer")
	}

	// And the same peer twice sends nothing the second time, which is the
	// point of a watermark.
	again := filepath.Join(tmp, "toMiniAgain")
	if err := runSync(dir, []string{"export", again, "--peer", "mini"}); err != nil {
		t.Fatal(err)
	}
	if n := batchRecordCount(t, again); n != 0 {
		t.Errorf("the same peer received %d records twice", n)
	}
}

func TestExportRejectsAPeerWithNoName(t *testing.T) {
	tmp := setupLocalIndex(t)
	err := runSync(os.Getenv("DEJA_INDEX_DIR"), []string{"export", filepath.Join(tmp, "out"), "--peer"})
	if err == nil {
		t.Fatal("--peer with no name was accepted")
	}
	if !strings.Contains(err.Error(), "--peer") {
		t.Errorf("the error does not name the flag: %v", err)
	}
}

func batchRecordCount(t *testing.T, dir string) int {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
	}
	return n
}

var _ = index.MachineName
