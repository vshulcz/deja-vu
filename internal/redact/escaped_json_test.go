package redact

import (
	"strings"
	"testing"
)

// Agents paste nested JSON all day — curl output, a log line holding a payload,
// one tool's output quoted inside another's — and there every quote is escaped.
// The keyword patterns stopped matching, so a secret recognisable only by its
// key name went into the index whole (#1765).
func TestSecretsInEscapedJSONAreRedacted(t *testing.T) {
	for _, tc := range []struct{ in, keep string }{
		{`{\"api_key\": \"8f14e45fceea167a5a36dedd4bea2543\"}`, "api_key"},
		{`{\"password\": \"hunter2hunter2hunter2\"}`, "password"},
		{`{\"aws_secret_access_key\": \"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\"}`, "aws_secret_access_key"},
		{`{\"пароль\": \"8f14e45fceea167a5a36dedd4bea2543\"}`, "пароль"},
		{`{\\"api_key\\": \\"8f14e45fceea167a5a36dedd4bea2543\\"}`, "api_key"},
	} {
		got, _ := Text(tc.in)
		if !strings.Contains(got, "[redacted") {
			t.Errorf("not redacted: %s -> %s", tc.in, got)
		}
		if !strings.Contains(got, tc.keep) {
			t.Errorf("the key name went with it: %s -> %s", tc.in, got)
		}
		if strings.Contains(got, "8f14e45fceea167a5a36dedd4bea2543") || strings.Contains(got, "hunter2hunter2") || strings.Contains(got, "wJalrXUtnFEMI") {
			t.Errorf("the value survived: %s -> %s", tc.in, got)
		}
	}

	// The quoted-prose pattern too, in both spellings — and a value that holds
	// backslashes of its own still counts, which is where narrowing the value
	// class to fix the escaped case would have cost a redaction.
	for _, in := range []string{
		`password is \"hunter2hunter2\"`,
		`password is "C:\Windows\path\secret"`,
		`password authentication failed for user "admin" with password "S3cr3tP@ssw0rd!"`,
	} {
		got, _ := Text(in)
		if !strings.Contains(got, "[redacted") {
			t.Errorf("quoted secret survived: %s -> %s", in, got)
		}
	}

	// Plain JSON is unchanged, and the near-misses stay near misses.
	plain, _ := Text(`{"api_key": "8f14e45fceea167a5a36dedd4bea2543"}`)
	if !strings.Contains(plain, "[redacted") {
		t.Errorf("plain json stopped being redacted: %s", plain)
	}
	for _, keep := range []string{
		`token = os.environ['MY_TOKEN']`,
		`the password prompt appeared twice`,
		`/usr/local/share/token/abcdefghijklmnop`,
		`password: hunter2`,
		`C:\Users\token\abcdefghijklmnopqrst`,
		`the pattern is token\s*[:=]\s*value`,
	} {
		if got, _ := Text(keep); got != keep {
			t.Errorf("a near miss was redacted: %q -> %q", keep, got)
		}
	}
}
