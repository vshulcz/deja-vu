package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAllowEverything(t *testing.T) {
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing.json"))
	p := Load()
	for _, act := range []string{ActivationSearch, ActivationMCP, ActivationAuto} {
		for _, proj := range []string{"deja-vu", "imported:mini/deja-vu"} {
			if !p.Allows(act, proj) {
				t.Fatalf("default policy must allow %s/%s", act, proj)
			}
		}
		if got := p.Describe(act); got != "local+imported" {
			t.Fatalf("Describe(%s) = %q", act, got)
		}
	}
}

func TestOriginClassification(t *testing.T) {
	cases := map[string]string{
		"deja-vu":                "local",
		"imported:mini/deja-vu":  "imported:mini",
		"imported:mini":          "imported",
		"imported:work/api/auth": "imported:work",
	}
	for proj, want := range cases {
		if got := Origin(proj); got != want {
			t.Fatalf("Origin(%q) = %q, want %q", proj, got, want)
		}
	}
}

func TestFileDeniesImportedOnAuto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"auto":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Load()
	if p.Allows(ActivationAuto, "imported:mini/deja-vu") {
		t.Fatal("auto must deny imported")
	}
	if !p.Allows(ActivationAuto, "deja-vu") || !p.Allows(ActivationMCP, "imported:mini/deja-vu") {
		t.Fatal("local auto and imported mcp must stay allowed")
	}
	if got := p.Describe(ActivationAuto); got != "local-only" {
		t.Fatalf("Describe(auto) = %q, want local-only", got)
	}
}

func TestPeerSpecificRuleWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"mcp":{"imported":true,"imported:untrusted":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Load()
	if p.Allows(ActivationMCP, "imported:untrusted/box") {
		t.Fatal("peer rule must deny untrusted")
	}
	if !p.Allows(ActivationMCP, "imported:mini/deja-vu") {
		t.Fatal("other peers stay allowed")
	}
	if got := p.Describe(ActivationMCP); got != "deny imported:untrusted" {
		t.Fatalf("Describe = %q", got)
	}
}

func TestEnvAliasStillWorks(t *testing.T) {
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	p := Load()
	if p.Allows(ActivationAuto, "imported:mini/deja-vu") {
		t.Fatal("env alias must deny imported on auto")
	}
	if !p.Allows(ActivationAuto, "deja-vu") || !p.Allows(ActivationSearch, "imported:mini/deja-vu") {
		t.Fatal("alias must only touch the auto path")
	}
	if got := p.Describe(ActivationAuto); got != "local-only" {
		t.Fatalf("Describe = %q", got)
	}
}

func TestMalformedFileFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Load().Allows(ActivationAuto, "imported:mini/x") {
		t.Fatal("malformed policy must not lock recall out")
	}
}

func TestFilterDropsBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)
	if err := os.WriteFile(path, []byte(`{"activations":{"search":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	items := []string{"deja-vu", "imported:mini/deja-vu", "other"}
	got := Filter(Load(), ActivationSearch, items, func(s string) string { return s })
	if len(got) != 2 || got[0] != "deja-vu" || got[1] != "other" {
		t.Fatalf("Filter = %v", got)
	}
}
