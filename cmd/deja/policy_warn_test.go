package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A trust policy that does not load withholds nothing, and the only place that
// said so was doctor: on the paths that actually answer, the guard went off in
// silence (#1088).
func TestSearchWarnsWhenTheTrustPolicyDoesNotLoad(t *testing.T) {
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	if err := os.WriteFile(pol, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, pol) {
		t.Errorf("the warning does not name the policy file:\n%s", errOut)
	}
	if !strings.Contains(errOut, "not valid JSON") {
		t.Errorf("the warning does not name the cause:\n%s", errOut)
	}
	if !strings.Contains(errOut, "every origin activates") {
		t.Errorf("the warning does not say what is in force:\n%s", errOut)
	}
}

func TestSearchIsQuietWhenThePolicyLoadsOrIsAbsent(t *testing.T) {
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "trust policy") {
		t.Errorf("no policy file at all still warned:\n%s", errOut)
	}

	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut, err = captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "trust policy") {
		t.Errorf("a policy that loads still warned:\n%s", errOut)
	}
}

func TestPolicyWarningNamesPermissionsRatherThanTheSyscall(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not deny reads here")
	}
	hermeticEnv(t)
	pol := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", pol)
	if err := os.WriteFile(pol, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pol, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pol, 0o600) })

	errOut, err := captureRunStderr(t, "capstan pawl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "check its permissions") {
		t.Errorf("the warning does not say what to change:\n%s", errOut)
	}
	if strings.Contains(errOut, "open ") {
		t.Errorf("the warning passes the syscall through:\n%s", errOut)
	}
}
