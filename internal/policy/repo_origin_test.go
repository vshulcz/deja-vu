package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoOriginCanBeDenied(t *testing.T) {
	if got := Origin("repo:acme/api"); got != "repo:acme/api" {
		t.Errorf("Origin = %q", got)
	}
	p := Policy{Activations: map[string]map[string]bool{
		ActivationAuto:   {"repo": false},
		ActivationSearch: {"repo": false, "repo:acme/api": true},
	}}
	if p.Allows(ActivationAuto, "repo:acme/api") {
		t.Error("repo deny did not hold on auto")
	}
	if !p.Allows(ActivationAuto, "acme/api") || !p.Allows(ActivationAuto, "imported:acme/api") {
		t.Error("repo deny reached other origins")
	}
	if !p.Allows(ActivationSearch, "repo:acme/api") || p.Allows(ActivationSearch, "repo:acme/web") {
		t.Error("the repository's own rule should win over repo")
	}
	if !p.Allows(ActivationMCP, "repo:acme/api") {
		t.Error("an activation with no rule allows repo memory")
	}
	if got := p.Describe(ActivationAuto); got != "deny repo" {
		t.Errorf("Describe = %q", got)
	}
	if p.AllowsEgress("repo:acme/api") {
		t.Error("egress needs every activation to agree")
	}
}

func TestRepoOriginIsAKnownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"activations":{"auto":{"repo":false,"repo:acme/api":true,"bogus":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", path)
	_, unknown, err := Diagnose()
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 || unknown[0] != "auto.bogus" {
		t.Errorf("unknown = %v", unknown)
	}
}
