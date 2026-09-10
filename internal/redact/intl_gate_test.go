package redact

import (
	"strings"
	"testing"
)

// The international key=value pattern is an alternation of Cyrillic, Chinese,
// Japanese and Korean words, and it was gated on the whole hint list — so an
// English transcript that says "token" ran it over every message. Measured
// over 6.1 MB of real transcript text, that was 2227 ms of the 4523 ms
// redaction spends, on text the pattern cannot match a byte of.
func TestTheInternationalPatternRunsOnItsOwnWords(t *testing.T) {
	// Still redacted, in each script the pattern is written for.
	for _, secret := range []string{
		"пароль: hunter2secretvalue123",
		"токен = abcdefghijklmnopqrstuvwx",
		"密码: supersecretvalue1234",
		"contraseña: supersecretvalue1234",
		"비밀번호: supersecretvalue1234",
	} {
		got, counts := Text(secret)
		if strings.Contains(got, "supersecretvalue1234") || strings.Contains(got, "hunter2secretvalue123") ||
			strings.Contains(got, "abcdefghijklmnopqrstuvwx") {
			t.Errorf("%q was stored in the clear: %q", secret, got)
		}
		if counts.Total() == 0 {
			t.Errorf("%q was not counted as a redaction", secret)
		}
	}
	// And the English gate still catches the English shapes, which is what the
	// other patterns are for.
	got, _ := Text("api_key: abcdefghijklmnopqrstuvwx")
	if strings.Contains(got, "abcdefghijklmnopqrstuvwx") {
		t.Errorf("an English assignment was stored in the clear: %q", got)
	}
}

// The gate is a necessary condition, so a text with none of those words does
// not reach the pattern at all — and one that has the word without an
// assignment still does not.
func TestTheInternationalPatternIsNotRunOnEnglishText(t *testing.T) {
	before := intlPatternRuns.Load()
	Text("api_key: abcdefghijklmnopqrstuvwx and a token: abcdefghijklmnopqrstuvwx")
	if intlPatternRuns.Load() != before {
		t.Error("an English assignment ran the Cyrillic, Chinese, Japanese and Korean alternation")
	}
	Text("пароль: hunter2secretvalue123")
	if intlPatternRuns.Load() == before {
		t.Error("a Russian assignment did not reach the pattern written for it")
	}
}

func TestTheInternationalGateNeedsBothWordAndAssignment(t *testing.T) {
	if kvAssignmentNearbyHints(strings.ToLower("we spent the whole token budget on this"), kvIntlHints) {
		t.Error("an English sentence opened the international gate")
	}
	if kvAssignmentNearbyHints(strings.ToLower("пароль от вина был простой"), kvIntlHints) {
		t.Error("a sentence with no assignment opened the gate")
	}
	if !kvAssignmentNearbyHints(strings.ToLower("пароль: hunter2secretvalue123"), kvIntlHints) {
		t.Error("a real assignment did not open the gate")
	}
}
