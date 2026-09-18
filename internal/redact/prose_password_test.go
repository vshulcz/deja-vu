package redact

import (
	"strings"
	"testing"
)

// A person telling an agent a password does not write a delimiter, and every
// other rule here wants one. Found by seeding fake credentials into a
// transcript and asking every surface for them: `deja show` and `deja ctx`
// read this one back in the clear (#3729).
func TestProsePasswordIsRedacted(t *testing.T) {
	for _, tc := range []struct{ in, gone string }{
		{"also the admin password is hunter2-not-a-real-password, do not put it in code", "hunter2-not-a-real-password"},
		{"The password is P@ssw0rd-2026 for the staging box", "P@ssw0rd-2026"},
		{"passphrase was correct-horse-2026 when we set it up", "correct-horse-2026"},
		{"пароль это БазаПароль2026, поменяем позже", "БазаПароль2026"},
	} {
		got, counts := Text(tc.in)
		if strings.Contains(got, tc.gone) {
			t.Errorf("the value is still there: %q", got)
		}
		if counts.Total() == 0 {
			t.Errorf("nothing counted for %q", tc.in)
		}
	}
}

// And the sentences that are not credentials stay as they were: the value has
// to look chosen — a digit or a symbol — and it stops at the first space.
func TestProsePasswordLeavesProseAlone(t *testing.T) {
	for _, s := range []string{
		"the password is wrong",
		"the password is correct",
		"the password is the same as staging",
		"the password is empty",
		"the password is in the vault",
		"пароль это неправильный",
		"the passphrase is stored in 1Password",
	} {
		got, _ := Text(s)
		if got != s {
			t.Errorf("prose was masked: %q -> %q", s, got)
		}
	}
}
