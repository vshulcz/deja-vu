package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A policy file that will not parse warns on every command, because loading
// fails open and the rule someone wrote stops holding (#1088). A rule that
// parses and names something deja never consults fails open the same way — the
// project stays in every answer — and only `doctor` said so (#2452).
func TestARuleThatDoesNothingSaysSoWhereRecallHappens(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)

	// The shape someone writes when they mean "keep this project out": the
	// keys deja consults are origins, not projects.
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"work/secretclient":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnBrokenPolicy("search", &out)
	got := out.String()
	if !strings.Contains(got, "work/secretclient") {
		t.Errorf("the rule that does nothing is named nowhere but doctor:\n%q", got)
	}
	if !strings.Contains(got, "does nothing") && !strings.Contains(got, "no effect") {
		t.Errorf("the warning does not say the rule is inert:\n%q", got)
	}

	// A policy that says what deja understands says nothing extra.
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	warnBrokenPolicy("search", &out)
	if out.Len() != 0 {
		t.Errorf("a policy deja understands produced a warning:\n%q", out.String())
	}

	// And doctor keeps reporting it in full rather than being warned at.
	out.Reset()
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"work/secretclient":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	warnBrokenPolicy("doctor", &out)
	if out.Len() != 0 {
		t.Errorf("doctor was warned at about the file it prints in full:\n%q", out.String())
	}
}
