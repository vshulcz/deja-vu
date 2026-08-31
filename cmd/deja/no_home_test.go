package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNoHomeHelperProcess is one deja invocation in a process of its own, with
// no home directory and a working directory of the caller's choosing. In-process
// is not enough here: the writes this is about come from a detached warmup the
// parent does not wait for, so a glob taken when the call returns sees nothing
// whether or not the guard is there.
func TestNoHomeHelperProcess(t *testing.T) {
	args, running := os.LookupEnv("DEJA_NO_HOME_ARGS")
	if !running {
		return
	}
	// TestMain gives every test in this package a home of its own, so the
	// child has to take it back off before it asks deja anything.
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "DEJA_INDEX_DIR"} {
		if err := os.Unsetenv(k); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	var argv []string
	if args != "" {
		argv = strings.Split(args, "\x1f")
	}
	code := 0
	if err := run(argv); err != nil {
		code = 1
	}
	os.Exit(code)
}

// runWithNoHome runs deja in an empty directory with nothing to say where its
// index goes, and answers with the exit code and whatever it left behind.
func runWithNoHome(t *testing.T, args ...string) (int, []string) {
	t.Helper()
	wd := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNoHomeHelperProcess$")
	cmd.Dir = wd
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(),
		"DEJA_NO_HOME_ARGS="+strings.Join(args, "\x1f"),
		"HOME=", "USERPROFILE=", "XDG_CONFIG_HOME=", "XDG_CACHE_HOME=", "DEJA_INDEX_DIR=")
	out, runErr := cmd.CombinedOutput()
	if testing.Verbose() {
		t.Logf("%v said: %s", args, out)
	}
	code := 0
	if err := runErr; err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	// The warmup is detached, so what it writes lands after the process is
	// gone: wait for it rather than racing it. Short, because every clean arm
	// pays the whole wait.
	var left []string
	for i := 0; i < 8; i++ {
		found, err := filepath.Glob(filepath.Join(wd, "*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(found) > 0 {
			left = found
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i, p := range left {
		left[i] = strings.TrimPrefix(p, wd+string(filepath.Separator))
	}
	return code, left
}

// Every path deja writes hangs off the home directory, and it answers "" when
// there is none — so `filepath.Join("", ".cache", "deja")` is `.cache/deja`,
// and deja built its index in whatever directory it was run from. #1690 fixed
// install; the rest of the commands, including the ones an agent runs
// unattended, still wrote relative (#1692).
func TestWithNoHomeNothingIsWrittenWhereDejaHappensToRun(t *testing.T) {
	for _, c := range []struct {
		args []string
		// A hook must never cost a turn, so it declines quietly; a command
		// somebody typed says why; a command that writes nothing anyway just
		// runs.
		wantCode int
	}{
		{args: []string{"search", "foo"}, wantCode: 1},
		{args: []string{"retry", "budget"}, wantCode: 1},
		{args: []string{"show", "1"}, wantCode: 1},
		{args: []string{"last"}, wantCode: 1},
		{args: []string{"index"}, wantCode: 1},
		{args: []string{"aider"}, wantCode: 1},
		{args: []string{"goose"}, wantCode: 1},
		{args: []string{"mcp"}, wantCode: 1},
		{args: []string{"hook-context"}},
		{args: []string{"hook-prompt"}},
		{args: []string{"hook-tool"}},
		{args: []string{"hook-precompact"}},
		{args: []string{"statusline"}},
		// These write nothing without a home, and refusing them takes away the
		// commands a reader needs precisely when deja cannot find one.
		{args: []string{"version"}},
		// Both spellings of the same command answer the same way, and neither
		// falls through to the bare-query path, which builds an index.
		{args: []string{"--version"}},
		{args: []string{"-version"}},
		// Not a command at all: allowlisting it let it past the guard, miss
		// the dispatch map and land in the query path.
		{args: []string{"-v"}, wantCode: 1},
		// No arguments at all, which is the brief on a terminal.
		{args: nil, wantCode: 1},
		{args: []string{"help"}},
		{args: []string{"completion", "bash"}},
		{args: []string{"sources"}},
		{args: []string{"doctor"}},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			code, left := runWithNoHome(t, c.args...)
			if code != c.wantCode {
				t.Errorf("exit %d, want %d", code, c.wantCode)
			}
			if len(left) > 0 {
				t.Errorf("deja wrote into the working directory: %v", left)
			}
		})
	}
}

// install keeps its own refusal: it is the one that can say which account to
// wire, and the general one would send the reader after DEJA_INDEX_DIR, which
// does not help install at all.
func TestInstallKeepsItsOwnRefusal(t *testing.T) {
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "DEJA_INDEX_DIR"} {
		t.Setenv(k, "")
	}
	if stop, err := homelessRefusal("install"); stop {
		t.Errorf("install was refused by the general guard: %v", err)
	}
	stop, err := homelessRefusal("search")
	if !stop || err == nil {
		t.Fatal("a command that writes an index was allowed to run")
	}
	if !strings.Contains(err.Error(), "HOME") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// doctor is where the refusal sends the reader, so it runs without a home —
// but --deep takes the index lock first, which left a lock file in whatever
// directory they were standing in.
func TestDoctorDeepDoesNotTakeALockItCannotHold(t *testing.T) {
	code, left := runWithNoHome(t, "doctor", "--deep")
	if code == 0 {
		t.Error("deep verify of an index that cannot exist reported success")
	}
	if len(left) > 0 {
		t.Errorf("doctor --deep wrote into the working directory: %v", left)
	}
}
