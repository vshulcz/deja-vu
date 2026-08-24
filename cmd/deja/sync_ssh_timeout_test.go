package main

import (
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
		return "", nil
	}
	t.Cleanup(func() { sshRunner = old })

	sshRunner = func(name string, args ...string) (string, error) {
		got = append(got, append([]string{name}, args...))
		return "/tmp/deja-remote", nil
	}
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
