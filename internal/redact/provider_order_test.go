package redact

import (
	"strings"
	"testing"
)

// A provider key is labelled by its provider even when it arrives as a value
// in an assignment, which is how keys actually appear. The generic rules used
// to reach it first and call it `credential`, so deja could say a session had
// pasted *something* but not what (#536).
func TestAProviderKeyKeepsItsProviderInAnAssignment(t *testing.T) {
	long := strings.Repeat("A1b2C3d4", 5)
	for _, tc := range []struct {
		name, in, want string
	}{
		{"env assignment", "GITHUB_TOKEN=ghp_" + long, "github-token"},
		{"quoted api key", `api_key: "sk-` + long + `"`, "openai-key"},
		{"bearer header", "Authorization: Bearer sk-ant-" + long, "anthropic-key"},
		{"export", "export SLACK_TOKEN=xoxb-" + long, "slack-token"},
		{"stripe in json", `{"secret_key": "sk_live_` + long + `"}`, "stripe-key"},
	} {
		got, counts := Text(tc.in)
		if !strings.Contains(got, "[redacted:"+tc.want+"]") {
			t.Errorf("%s: %q masked as %q, want the %s label", tc.name, tc.in, got, tc.want)
		}
		if counts[tc.want] == 0 {
			t.Errorf("%s: counts = %v, want one %s", tc.name, counts, tc.want)
		}
		// The value is gone whichever rule fired, and it must not be masked
		// twice under two names.
		if strings.Contains(got, long) {
			t.Errorf("%s: the value survived: %q", tc.name, got)
		}
		if counts["credential"]+counts["quoted-secret"] > 0 && counts[tc.want] == 0 {
			t.Errorf("%s: counted as generic: %v", tc.name, counts)
		}
		if strings.Count(got, "[redacted:") != 1 {
			t.Errorf("%s: masked more than once: %q", tc.name, got)
		}
	}
}

// What the order must not change: a value with no provider shape is still
// caught by the assignment rules behind it.
func TestAPlainAssignedSecretIsStillMasked(t *testing.T) {
	got, counts := Text(`DB_PASSWORD="hunter2hunter2hunter2"`)
	if strings.Contains(got, "hunter2") {
		t.Fatalf("an ordinary assigned secret went through: %q", got)
	}
	if counts.Total() == 0 {
		t.Fatal("nothing was counted")
	}
}

// Every rule name this package writes has to be in the list a reader checks
// against, or a marker deja itself wrote gets dropped as a lookalike (#536).
func TestEveryKindProducedIsAKnownKind(t *testing.T) {
	long := strings.Repeat("A1b2C3d4", 5)
	samples := []string{
		"GITHUB_TOKEN=ghp_" + long,
		`api_key: "sk-` + long + `"`,
		"Authorization: Bearer sk-ant-" + long,
		"export SLACK_TOKEN=xoxb-" + long,
		`{"secret_key": "sk_live_` + long + `"}`,
		"npm_" + strings.Repeat("a1b2c3d4", 5),
		"gsk_" + long,
		"xai-" + long,
		"hf_" + long,
		"glpat-" + long,
		"AIza" + long,
		"psql postgres://svc:hunter2hunter2@db:5432/app",
		"aws_secret_access_key = " + long + "abcd",
		"AKIAIOSFODNN7EXAMPLE",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln",
		"Cookie: session=" + long,
		"mysql -uroot -phunter2 app",
		"sshpass -p hunter2 ssh host",
		"machine example.com login admin password hunter2",
		"-----BEGIN RSA PRIVATE KEY-----\n" + long + "\n-----END RSA PRIVATE KEY-----",
		`DB_PASSWORD="hunter2hunter2hunter2"`,
		"пароль: " + long,
		"TOKEN=" + long + "0000",
	}
	seen := map[string]bool{}
	for _, in := range samples {
		_, counts := Text(in)
		for kind := range counts {
			seen[kind] = true
			if !IsKind(kind) {
				t.Errorf("Text produced kind %q, which IsKind does not know", kind)
			}
		}
	}
	if len(seen) < 12 {
		t.Fatalf("the samples only exercised %d rules (%v), too few to be a guard", len(seen), seen)
	}
}
