package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installPipeHelperEnv is the home TestInstallPipeHelper runs in, and the
// switch that makes it run at all: a *_HELPER name is how this package talks
// to a copy of its own binary, and testenv scrubs every other DEJA_ name a
// child could be pointed at.
const installPipeHelperEnv = "DEJA_INSTALL_PIPE_HELPER"

// A stdout that closes early must cost deja its output, not the record of what
// it did (#5322). `deja install aider | head -1` reads one line and closes the
// pipe while the run still has its wiring to write: the config is already
// edited, and recordWiring — a deferred call — never gets there, so uninstall
// is handed a file it has no record of and puts back a file of deja's own
// making. Expected: the closed pipe loses lines, not state.
func TestInstallKeepsWiringWhenStdoutClosesEarly(t *testing.T) {
	hermeticEnv(t)
	home := homeDir()
	conf := filepath.Join(home, ".aider.conf.yml")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("read: CONVENTIONS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallPipeHelper$")
	cmd.Env = append(os.Environ(), installPipeHelperEnv+"="+home)
	cmd.Stdout, cmd.Stdin, cmd.Stderr = stdoutW, stdin, &said
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Only the child's ends are in its way now, and a helper that never
	// reaches the barrier is not worth waiting for.
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = stdoutW.Close()
	_ = stdin.Close()
	// One line, the way `head -1` takes one line — the pipe with it, so every
	// line the install prints from here is a write on a closed pipe. The
	// barrier is what keeps this from being a race: without the wait the
	// install starts before the pipe closes, and a child quick enough to
	// finish while the parent is still reading would record its wiring
	// normally and prove nothing.
	line, rerr := bufio.NewReader(stdout).ReadString('\n')
	_ = stdout.Close()
	if rerr != nil || strings.TrimSpace(line) != "ready" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("the helper never reached the install (%v); it said %q; stderr:\n%s",
			rerr, strings.TrimSpace(line), said.String())
	}
	// The install runs now, against a stdout nobody is reading.
	_ = stdinW.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("a closed stdout ended the run before it could record the wiring (%v); stderr:\n%s",
			err, said.String())
	}
	wiring, err := os.ReadFile(filepath.Join(home, ".config", "deja", "wiring.json"))
	if err != nil {
		t.Fatalf("the run left no wiring record: %v; stderr:\n%s", err, said.String())
	}
	if want := aiderReadWasKey + "read: CONVENTIONS.md"; !strings.Contains(string(wiring), want) {
		t.Errorf("wiring.json holds no record of the read: line install promoted (%s):\n%s", want, wiring)
	}
	// The record is what uninstall reads, so the reader's own line comes back.
	var out string
	var runErr error
	captureStderr(t, func() { out, runErr = captureRun(t, "uninstall", "aider") })
	if runErr != nil {
		t.Fatalf("uninstall: %v\n%s", runErr, out)
	}
	restored, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("uninstall left the reader without a config: %v", err)
	}
	if string(restored) != "read: CONVENTIONS.md\n" {
		t.Errorf("the config did not come back as the reader wrote it:\n%q", restored)
	}
}

// TestInstallPipeHelper is the deja process the test above drives. TestMain
// hands this process a home of its own; the one it wants is the test's, which
// holds the .aider.conf.yml install is about to edit.
func TestInstallPipeHelper(t *testing.T) {
	home := os.Getenv(installPipeHelperEnv)
	if home == "" {
		return
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// At the barrier, and saying so. The test closes the pipe only once this
	// line has been read, and lets the install start only after that.
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run([]string{"install", "aider", "--no-index"}); err != nil {
		fmt.Fprintln(os.Stderr, "deja:", err)
		os.Exit(1)
	}
	// os.Exit rather than a return: the framework prints its summary on
	// stdout, which is the pipe the test has just closed. Letting that line
	// decide the exit status would measure the framework, not the install.
	os.Exit(0)
}
