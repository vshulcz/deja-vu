package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The document opens with a rule — object-shaped responses carry
// `schema_version` — and then lists the exceptions to it. That list is prose,
// and prose drifts: `deja log --last --json` is an object without the field and
// was in neither half, which is how it was found. Here the two are checked
// against each other, so a new surface has to join one side or the other.
func TestWhichJSONSurfacesCarryASchemaVersion(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	policy := docSection(t, string(doc), "## Stability policy")

	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigestPolicy(dir, usage.KindHook, "the injected block", 2, 4000, "local-only")

	for _, c := range []struct {
		name        string
		args        []string
		versionable bool
	}{
		{"deja log --json", []string{"--json"}, false},
		{"deja log --last --json", []string{"--last", "--json"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			if err := runLogTo(&b, dir, c.args); err != nil {
				t.Fatal(err)
			}
			var v any
			if err := json.Unmarshal([]byte(b.String()), &v); err != nil {
				t.Fatalf("not JSON: %q", strings.TrimSpace(b.String()))
			}
			m, isObject := v.(map[string]any)
			_, hasVersion := m["schema_version"]

			if hasVersion != c.versionable {
				t.Errorf("schema_version present = %v, want %v", hasVersion, c.versionable)
			}
			// A shape that carries no version has to be named in the policy
			// section, whether it is an array or an object.
			if !hasVersion && !strings.Contains(policy, "`"+c.name+"`") {
				t.Errorf("%s carries no schema_version and the stability policy does not say so", c.name)
			}
			if isObject && !hasVersion && !strings.Contains(policy, "one record") {
				t.Errorf("an object without a version needs the policy to say which record shape it is")
			}
		})
	}
}
