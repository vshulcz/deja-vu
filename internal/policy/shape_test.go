package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func policyAt(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", p)
}

// Diagnose caught a typo inside a structure that was otherwise right, and said
// nothing about a file whose whole shape is wrong — which is the one someone
// writes from memory or from another tool's config. It parses, denies nothing,
// and every surface stays silent (#2504).
func TestDiagnoseNamesAKeyDejaDoesNotConsult(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "a shape from somewhere else",
			body: `{"rules":[{"project":"work","auto":"deny"}]}`,
			want: "rules",
		},
		{
			name: "the right idea under the wrong name",
			body: `{"activation":{"auto":{"local":false}}}`,
			want: "activation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyAt(t, tc.body)
			exists, unknown, err := Diagnose()
			if err != nil || !exists {
				t.Fatalf("exists=%v err=%v", exists, err)
			}
			if !strings.Contains(strings.Join(unknown, " "), tc.want) {
				t.Errorf("a file that denies nothing is reported as %v; want it to name %q", unknown, tc.want)
			}
		})
	}
}

// The keys deja does consult stay quiet.
func TestDiagnoseIsQuietOnAFileItUnderstands(t *testing.T) {
	policyAt(t, `{"activations":{"auto":{"local":false}},"ignore":["/tmp/scratch"]}`)
	_, unknown, err := Diagnose()
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 0 {
		t.Errorf("a policy deja reads in full is reported as unknown: %v", unknown)
	}
}
