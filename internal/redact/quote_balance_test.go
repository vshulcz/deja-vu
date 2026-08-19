package redact

import "testing"

// A masked value keeps the quoting of the value it replaced. The line a secret
// arrives on is usually a config or a JSON fragment someone will read, or paste
// back, and dropping the closing quote leaves it unparseable — the mask is
// meant to hide the value, not to break the line around it.
//
// Measured by removing the guard: with closingQuote returning "", every quoted
// secret came back with an opening quote and no closing one, and no test in
// this package failed.
func TestRedactedValueKeepsItsQuoting(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"aws secret in double quotes",
			`aws_secret_access_key = "wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY"`,
			`aws_secret_access_key = "[redacted:aws-secret]"`,
		},
		{
			"aws secret bare",
			`aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`,
			`aws_secret_access_key = [redacted:aws-secret]`,
		},
		{
			"credential in single quotes",
			`password: 'hunter2hunter2hunter2hunter2'`,
			`password: '[redacted:credential]'`,
		},
		{
			"credential in double quotes",
			`api_key="abcdefghijklmnopqrstuvwxyz0123456789"`,
			`api_key="[redacted:credential]"`,
		},
		{
			"credential bare",
			`api_key=abcdefghijklmnopqrstuvwxyz0123456789`,
			`api_key=[redacted:credential]`,
		},
		// A closing quote with nothing opening it is not the value's quoting:
		// echoing it would leave a line with one quote in it, which is worse
		// than the line that arrived.
		{
			"stray closing quote, aws",
			`aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY"`,
			`aws_secret_access_key = [redacted:aws-secret]`,
		},
		{
			"stray closing quote, credential",
			`api_key=abcdefghijklmnopqrstuvwxyz0123456789"`,
			`api_key=[redacted:credential]`,
		},
		{
			"stray single quote, credential",
			`api_key=abcdefghijklmnopqrstuvwxyz0123456789'`,
			`api_key=[redacted:credential]`,
		},
		// Mismatched quotes are whatever the line had: the mask replaces the
		// value, not the punctuation around it, so the closing character is
		// the one that closed it and not a copy of the one that opened it.
		{
			"opened double, closed single",
			`api_key="abcdefghijklmnopqrstuvwxyz0123456789'`,
			`api_key="[redacted:credential]'`,
		},
		{
			"opened single, closed double",
			`api_key='abcdefghijklmnopqrstuvwxyz0123456789"`,
			`api_key='[redacted:credential]"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, counts := Text(tc.in)
			if counts.Total() == 0 {
				t.Fatalf("wrong fixture, nothing was redacted: %q", got)
			}
			if got != tc.want {
				t.Errorf("Text(%q) =\n  %q\nwant\n  %q", tc.in, got, tc.want)
			}
		})
	}
}
