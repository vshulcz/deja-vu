package redact

import (
	"strings"
	"testing"
)

// Which spelling a tool happened to print decided whether the value was stored
// in the clear: the gate in front of the quoted rule tested `api key`, `api_key`
// and `apikey` while its own pattern also accepts `api-key`, and the pattern
// had no space in it while the gate did. So `api_key "…"` was masked and
// `api-key "…"` was not (#3614).
//
// The colon form is the other half: a header or a config line — `x-api-key:
// "s3cretvalue"` — is not prose, and the key-value rule that owns assignments
// starts at sixteen characters of value, which is the floor the quoted rule
// exists to get under.
func TestEverySpellingOfAnApiKeyIsMasked(t *testing.T) {
	const secret = "s3cretvalue123"
	for _, in := range []string{
		`api-key "` + secret + `"`,
		`api_key "` + secret + `"`,
		`apikey "` + secret + `"`,
		`api key "` + secret + `"`,
		`API-KEY: "` + secret + `"`,
		`x-api-key: "` + secret + `"`,
		`api-key = '` + secret + `'`,
		"api-key: `" + secret + "`",
	} {
		got, counts := Text(in)
		if strings.Contains(got, secret) {
			t.Errorf("secret survived: %q -> %q", in, got)
		}
		if !strings.Contains(got, "[redacted:") || len(counts) == 0 {
			t.Errorf("nothing was marked as removed: %q -> %q %v", in, got, counts)
		}
	}
}

// And the value a key-value rule already masked keeps that rule's marker: the
// colon rule runs after them, so a long assigned value is not masked twice
// under a second name.
func TestAnAssignedSecretKeepsOneMarker(t *testing.T) {
	for _, in := range []string{
		`api_key="abcdefghijklmnopqrstuvwxyz0123456789"`,
		`password: 'hunter2hunter2hunter2hunter2'`,
	} {
		got, _ := Text(in)
		if n := strings.Count(got, "[redacted:"); n != 1 {
			t.Errorf("%q came back with %d markers: %q", in, n, got)
		}
		if strings.Contains(got, "quoted-secret") {
			t.Errorf("the assignment rule's marker was replaced: %q -> %q", in, got)
		}
	}
}

// The quotes are still what makes the rule safe: a name and a colon with no
// quoted value behind them is ordinary prose.
func TestTheColonFormLeavesProseAlone(t *testing.T) {
	for _, in := range []string{
		`api-key: see the vault entry`,
		`token: expired, re-run the login`,
		`password: the policy is in the wiki`,
		`api key rotation is due in April`,
	} {
		got, _ := Text(in)
		if got != in {
			t.Errorf("altered ordinary prose:\n  in  %q\n  out %q", in, got)
		}
	}
}
