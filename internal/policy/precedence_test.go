package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The policy decides which memory may activate where. Getting precedence wrong
// is the kind of failure nobody reports: a peer someone deliberately blocked
// keeps being injected, and the receipt still says the rule was applied.
func TestAllowsMostSpecificRuleWins(t *testing.T) {
	p := Policy{Activations: map[string]map[string]bool{
		ActivationAuto: {
			"imported:laptop": true,
			"imported":        false,
			"*":               true,
		},
	}}
	for project, want := range map[string]bool{
		"imported:laptop/api": true,  // named peer beats the imported default
		"imported:mini/api":   false, // any other peer takes the imported rule
		// The marker is the "imported:" prefix — a local project happening to
		// be called "imported" is still local.
		"imported:":     false,
		"imported":      true,
		"local-project": true, // "*" covers what no rule names
	} {
		if got := p.Allows(ActivationAuto, project); got != want {
			t.Fatalf("Allows(%q) = %v, want %v", project, got, want)
		}
	}
	// An activation with no rules allows everything: a policy file that
	// mentions one path must not silently gate the others.
	if !p.Allows(ActivationMCP, "imported:mini/api") {
		t.Fatal("an unmentioned activation blocked memory")
	}
}

func TestOriginClassifiesPeers(t *testing.T) {
	for project, want := range map[string]string{
		"api":                 "local",
		"":                    "local",
		"imported:laptop/api": "imported:laptop",
		"imported:laptop":     "imported",
		"imported:":           "imported",
		"imported:mini/a/b":   "imported:mini",
		"not-imported:laptop": "local",
	} {
		if got := Origin(project); got != want {
			t.Fatalf("Origin(%q) = %q, want %q", project, got, want)
		}
	}
}

// Describe names the rule in receipts and `deja log`, so the audit trail
// explains itself. A wrong name there is worse than none: it claims a policy
// was applied that was not.
func TestDescribeNamesTheRuleSet(t *testing.T) {
	for name, tc := range map[string]struct {
		p    Policy
		want string
	}{
		"no rules":      {Policy{}, "local+imported"},
		"local only":    {Policy{Activations: map[string]map[string]bool{ActivationAuto: {"imported": false}}}, "local-only"},
		"allows all":    {Policy{Activations: map[string]map[string]bool{ActivationAuto: {"*": true}}}, "local+imported"},
		"denies a peer": {Policy{Activations: map[string]map[string]bool{ActivationAuto: {"imported:mini": false}}}, "deny imported:mini"},
	} {
		if got := tc.p.Describe(ActivationAuto); got != tc.want {
			t.Fatalf("%s: Describe = %q, want %q", name, got, tc.want)
		}
	}
	// Several denials are listed in a stable order, or the same policy reads
	// differently from one receipt to the next.
	p := Policy{Activations: map[string]map[string]bool{ActivationAuto: {"imported:mini": false, "imported:laptop": false}}}
	first := p.Describe(ActivationAuto)
	for i := 0; i < 20; i++ {
		if got := p.Describe(ActivationAuto); got != first {
			t.Fatalf("description varies between calls: %q then %q", first, got)
		}
	}
	if !strings.Contains(first, "imported:laptop") || !strings.Contains(first, "imported:mini") {
		t.Fatalf("both denials should be named: %q", first)
	}
}

func TestPathFollowsItsOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_POLICY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := Path(); !strings.HasPrefix(got, home) {
		t.Fatalf("Path() = %q, outside the home directory", got)
	}
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got := Path(); !strings.HasPrefix(got, xdg) {
		t.Fatalf("XDG_CONFIG_HOME ignored: %q", got)
	}
	explicit := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", explicit)
	if got := Path(); got != explicit {
		t.Fatalf("DEJA_POLICY_FILE ignored: %q", got)
	}
}

// A policy file that cannot be read must not silently block everything, and
// must not silently allow everything either — the safe default is the
// documented one: allow, since deja is local-first and the file is optional.
func TestLoadFallsBackToTheDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	broken := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", broken)
	if err := os.WriteFile(broken, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load()
	if !p.Allows(ActivationAuto, "anything") {
		t.Fatal("an unreadable policy file blocked recall")
	}
	// And an absent file behaves the same way.
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(home, "absent.json"))
	if !Load().Allows(ActivationMCP, "imported:mini/api") {
		t.Fatal("a missing policy file blocked recall")
	}
}
