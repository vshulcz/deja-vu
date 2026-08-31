package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// The trust policy decides what recall may hand over, and its path is built
// from the home directory — which answers "" when there is none, so
// `filepath.Join("", ".config")` is `.config`. deja then read
// `./.config/deja/policy.json`: a file in whatever repository it happened to
// be run from was read as the reader's own trust policy (#2785).
func TestAPolicyIsNotReadFromWhereverDejaHappensToRun(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "DEJA_POLICY_FILE"} {
		t.Setenv(k, "")
	}
	dir := filepath.Join(wd, ".config", "deja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A rule that would withhold everything, sitting in a checkout.
	body := `{"activations":{"search":{"*":false},"auto":{"*":false},"mcp":{"*":false}}}`
	if err := os.WriteFile(filepath.Join(dir, "policy.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if p := Path(); p != "" && !filepath.IsAbs(p) {
		t.Errorf("the policy path is relative to the working directory: %q", p)
	}
	if !Load().Allows(ActivationSearch, "anything") {
		t.Error("a policy file from the working directory decided what recall may hand over")
	}
	exists, _, err := Diagnose()
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if exists {
		t.Error("deja reported a trust policy it found in the working directory")
	}
}

// And an absolute path is still read, whichever variable named it.
func TestAPolicyNamedByAnAbsolutePathIsStillRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", path)
	if Load().Allows(ActivationSearch, "anything") {
		t.Error("a policy deja was pointed at was ignored")
	}
	t.Setenv("DEJA_POLICY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "deja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja", "policy.json"), []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if Load().Allows(ActivationSearch, "anything") {
		t.Error("a policy under an absolute XDG_CONFIG_HOME was ignored")
	}
}

// A relative XDG_CONFIG_HOME is not the reader saying "read nothing": it is a
// value the spec says to ignore, and the home directory is still there.
func TestARelativeConfigHomeFallsBackToTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_POLICY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "relconf")
	if err := os.MkdirAll(filepath.Join(home, ".config", "deja"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"activations":{"search":{"*":false}}}`
	if err := os.WriteFile(filepath.Join(home, ".config", "deja", "policy.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := Path(), filepath.Join(home, ".config", "deja", "policy.json"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if Load().Allows(ActivationSearch, "anything") {
		t.Error("the reader's own policy was skipped over a relative XDG_CONFIG_HOME")
	}
}
