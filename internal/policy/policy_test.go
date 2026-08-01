package policy

import (
	"os"
	"path/filepath"
	"strings"
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

// Load falls back to the permissive default on any error, so a malformed file
// changed nothing and said nothing (#661). Diagnose is what doctor reads.
func TestDiagnose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)

	if exists, unknown, err := Diagnose(); exists || err != nil || unknown != nil {
		t.Fatalf("no file: exists=%v err=%v unknown=%v", exists, err, unknown)
	}
	if err := os.WriteFile(path, []byte("{ oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exists, _, err := Diagnose(); !exists || err == nil {
		t.Fatalf("malformed: exists=%v err=%v", exists, err)
	}
	// Rules that name something deja never consults are silently doing nothing,
	// which reads exactly like a rule that works.
	body := `{"activations":{"auto":{"nosuchorigin":false},"nosuch":{"local":false},"mcp":{"imported:peer1":false,"local":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	exists, unknown, err := Diagnose()
	if !exists || err != nil {
		t.Fatalf("valid: exists=%v err=%v", exists, err)
	}
	want := []string{"activation nosuch", "auto.nosuchorigin"}
	if strings.Join(unknown, "|") != strings.Join(want, "|") {
		t.Errorf("unknown = %v, want %v", unknown, want)
	}
}

// A path that exists but cannot be read as a file — the config directory left
// where the file should be — is neither "no policy" nor "malformed policy".
func TestDiagnoseUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	asDir := filepath.Join(dir, "policy.json")
	if err := os.MkdirAll(asDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", asDir)
	exists, _, err := Diagnose()
	if !exists || err == nil {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
}
