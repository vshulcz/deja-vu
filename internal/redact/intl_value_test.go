package redact

import (
	"strings"
	"testing"
)

// A secret whose value is not ASCII. Every key-value pattern here ends in
// `[A-Za-z0-9/+=._-]{16,}`, so a password written in the alphabet its owner
// types in was stored in the clear whatever the key word and whatever its
// length — #1319 widened the key words to the languages people write in and
// left the value class where it was (#3587).
func TestAPasswordInSomebodyElsesAlphabetIsRedacted(t *testing.T) {
	for _, line := range []string{
		"пароль: БазаПароль2026",
		"пароль: ОченьДлинныйПароль2026",
		"пароль от стейджа: Стейдж2026Пароль",
		"password: ОченьДлинныйПароль2026",
		"password: 非常に長いパスワード2026",
		"密码: 数据库密码2026年",
		"비밀번호: 데이터베이스2026",
		"token: Токен2026Значение",
		"password: 🔥SecretValue2026🔥",
	} {
		got, counts := Text(line)
		if !strings.Contains(got, "[redacted") {
			t.Errorf("the value survived: %q -> %q", line, got)
		}
		if counts["credential"] == 0 {
			t.Errorf("nothing was counted for %q", line)
		}
	}
}

// The digit is what keeps this away from prose: a value class wide enough for
// Cyrillic is wide enough for an ordinary word.
func TestProseAfterAKeyWordIsLeftAlone(t *testing.T) {
	for _, line := range []string{
		"пароль: неправильный",
		"пароль не подошёл, пробую снова",
		"password: аутентификация",
		"the passphrase is correct",
		"токен протух",
		"密码错误",
		"password: переменная",
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("prose was redacted: %q -> %q", line, got)
		}
	}
}

// And a placeholder stays readable, the way it does for the flag form.
func TestANonASCIIPlaceholderIsLeftAlone(t *testing.T) {
	for _, line := range []string{
		"пароль: $DB_PASSWORD",
		"пароль: <ваш-пароль-2026>",
		"password: ${ПАРОЛЬ_2026}",
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("a placeholder was redacted: %q -> %q", line, got)
		}
	}
}

// The ASCII patterns are untouched: what they took before, they still take,
// and what they left alone stays alone.
func TestTheASCIIRulesAreUnchanged(t *testing.T) {
	if got, _ := Text("api_key = 8f14e45fceea167a5a36dedd4bea2543"); !strings.Contains(got, "[redacted") {
		t.Errorf("an ascii secret stopped being redacted: %q", got)
	}
	for _, keep := range []string{
		"password: hunter2",
		"the password prompt appeared twice",
		"token = os.environ['MY_TOKEN']",
	} {
		if got, _ := Text(keep); got != keep {
			t.Errorf("a near miss was redacted: %q -> %q", keep, got)
		}
	}
}
