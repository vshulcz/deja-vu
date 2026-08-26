package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// policyBulletFor is the one list item in the stability policy that names a
// command. The check has to be on that item and not on the section: a phrase
// anywhere in the policy is satisfied by any sentence that happens to contain
// it, which is a test that passes on prose it never read.
func policyBulletFor(t *testing.T, policy, command string) string {
	t.Helper()
	for _, item := range strings.Split(policy, "\n- ") {
		if strings.Contains(item, "`"+command+"`") {
			return item
		}
	}
	return ""
}

// The document opens with a rule — object-shaped responses carry
// `schema_version` — and then lists the exceptions to it. That list is prose,
// and prose drifts: `deja log --last --json` is an object without the field and
// was in neither half, which is how it was found (#1975). Here the two are held
// against each other, so a surface has to join one side or the other.
func TestWhichJSONSurfacesCarryASchemaVersion(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	policy := docSection(t, string(doc), "## Stability policy")

	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigestPolicy(dir, usage.KindHook, "the injected block", 2, 4000, "local-only")
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "beta", "one.jsonl"), "svterm", []string{
		`{"type":"user","sessionId":"svterm","timestamp":"2026-08-20T10:00:00Z",` +
			`"message":{"role":"user","content":"pgbouncer runs in transaction mode"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name        string
		run         func(t *testing.T) string
		versionable bool
	}{
		{"deja log --json", func(t *testing.T) string { return runLogString(t, dir, "--json") }, false},
		{"deja log --last --json", func(t *testing.T) string { return runLogString(t, dir, "--last", "--json") }, false},
		{"deja last --json", func(t *testing.T) string { return captureRunString(t, "last", "3", "--json") }, true},
		{"deja show <exact-id> --harness <name> --json", func(t *testing.T) string {
			return captureRunString(t, "show", "svterm", "--harness", "claude", "--json")
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := c.run(t)
			var v any
			if err := json.Unmarshal([]byte(out), &v); err != nil {
				t.Fatalf("not JSON: %q", strings.TrimSpace(out))
			}
			m, isObject := v.(map[string]any)
			_, hasVersion := m["schema_version"]

			if hasVersion != c.versionable {
				t.Errorf("schema_version present = %v, want %v", hasVersion, c.versionable)
			}
			if hasVersion {
				return
			}
			// A shape that carries no version has to be named in the policy,
			// and its own bullet has to say that is what it means — otherwise a
			// command mentioned for any other reason reads as documented.
			bullet := policyBulletFor(t, policy, c.name)
			if bullet == "" {
				t.Fatalf("%s carries no schema_version and the stability policy does not name it", c.name)
			}
			if !strings.Contains(bullet, "schema_version") {
				t.Errorf("the policy names %s but its entry does not say it carries no schema_version:\n%s", c.name, bullet)
			}
			if isObject && !strings.Contains(bullet, "object-shaped") && !strings.Contains(bullet, "object it prints") {
				t.Errorf("%s is an object without a version and its entry does not say so:\n%s", c.name, bullet)
			}
		})
	}
}

func runLogString(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var b strings.Builder
	if err := runLogTo(&b, dir, args); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func captureRunString(t *testing.T, args ...string) string {
	t.Helper()
	out, err := captureRun(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
