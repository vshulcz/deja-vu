package main

import (
	"os"
	"strings"
	"testing"
)

// A laptop that is asleep neither answers nor refuses, so ssh waits out the
// system default — measured at 446 s on one host, and `deja sync` walks every
// machine it knows (#1772).
func TestSSHCarriesAConnectTimeout(t *testing.T) {
	var got [][]string
	old := sshRunner
	sshRunner = func(name string, args ...string) (string, error) {
		got = append(got, append([]string{name}, args...))
		return "/tmp/deja-remote", nil
	}
	t.Cleanup(func() { sshRunner = old })

	if _, err := sshCapture("somehost", "mktemp -d"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ssh was run %d times", len(got))
	}
	line := strings.Join(got[0], " ")
	if !strings.Contains(line, "ConnectTimeout=") {
		t.Errorf("ssh runs without a connect timeout: %s", line)
	}
	if !strings.Contains(line, "BatchMode=yes") {
		t.Errorf("ssh can stop on a password prompt nothing is reading: %s", line)
	}
	// The host and the command still arrive, in that order.
	if i, j := strings.Index(line, "somehost"), strings.Index(line, "mktemp -d"); i < 0 || j < 0 || i > j {
		t.Errorf("the call lost its shape: %s", line)
	}
}

// Every ssh and scp deja runs carries the options, including the cleanup call
// that removes the remote batch directory — a host that has gone away between
// the transfer and the cleanup would otherwise hold the sync open.
func TestEverySSHCallCarriesTheOptions(t *testing.T) {
	src, err := os.ReadFile("sync_ssh.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `sshRunner("ssh"`) && !strings.Contains(line, `sshRunner("scp"`) {
			continue
		}
		if !strings.Contains(line, "sshOpts()") && !strings.Contains(line, "scpArgs") {
			t.Errorf("sync_ssh.go:%d runs without the options: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// sshHostArg is what a test stub needs to read the host out of an ssh call now
// that every call carries options: the first argument that is neither an
// option nor an option's value.
func sshHostArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i]
	}
	return ""
}
