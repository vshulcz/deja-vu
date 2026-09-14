package redact

import (
	"strings"
	"testing"
)

// A password assigned with `=`, at the length people actually choose. The
// key-value floor of sixteen characters is right where the key word could be
// describing anything — `token`, `secret`, `key` — and wrong for this family,
// where the word names the value. Measured through an index pass, all of these
// reached `deja show` in the clear (#3588).
func TestAPasswordAssignedWithAnEqualsSignIsMaskedAtAnyLength(t *testing.T) {
	for _, tc := range []struct{ line, secret string }{
		{"jdbc:postgresql://db:5432/app?user=app&password=JdbcPass2026", "JdbcPass2026"},
		{"https://api.internal/login?user=app&password=QueryPass2026", "QueryPass2026"},
		{"kubectl create secret generic s --from-literal=password=K8sPass2026x", "K8sPass2026x"},
		{"DATABASE_PASSWORD=DotenvPass2026", "DotenvPass2026"},
		{"PGPASSWORD=Pg2026x psql -h db", "Pg2026x"},
		{"DB_PASS=ExportPass2026", "ExportPass2026"},
		{"export APP_PASS=Short12", "Short12"},
	} {
		got, counts := Text(tc.line)
		if strings.Contains(got, tc.secret) {
			t.Errorf("the value survived: %q -> %q", tc.line, got)
		}
		if counts["credential"] == 0 {
			t.Errorf("nothing was counted for %q -> %q", tc.line, got)
		}
	}
}

// A colon is how a sentence is written and `=` is how a value is assigned, so
// the older near-miss decision is untouched.
func TestAColonKeepsItsOlderReading(t *testing.T) {
	for _, line := range []string{
		"password: hunter2",
		"the password prompt appeared twice",
		"password authentication failed for user deploy",
		`{"password": "short"}`,
		`{"db_password":"Short1"}`,
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("a near miss was redacted: %q -> %q", line, got)
		}
	}
}

// `pass` lives inside other words, and the separator before it is what keeps
// this rule out of them.
func TestAWordEndingInPassIsNotAPassword(t *testing.T) {
	for _, line := range []string{
		"bypass=strict",
		"compass=north-facing",
		"surpass=previous-record",
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("a word was read as a password: %q -> %q", line, got)
		}
	}
}

// And a placeholder after the equals sign stays readable.
func TestAPlaceholderAfterTheEqualsSignIsLeftAlone(t *testing.T) {
	for _, line := range []string{
		"password=$DB_PASSWORD",
		"DATABASE_PASSWORD=${SECRET}",
		"password=<your-password>",
		"password=changeme",
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("a placeholder was redacted: %q -> %q", line, got)
		}
	}
}
