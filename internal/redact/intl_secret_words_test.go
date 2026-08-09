package redact

import (
	"strings"
	"testing"
)

// A secret is named in whatever language its owner types in. Every pattern here
// was English-only, so "пароль: <value>" was stored and shared in the clear
// while "password: <value>" was redacted.
func TestSecretWordsInOtherLanguagesAreRedacted(t *testing.T) {
	secrets := []string{
		"пароль: hunter2secretvalue123",
		"пароля: hunter2secretvalue123",
		"токен: ghp_abcdefghijklmnop12345",
		"секрет = supersecretvalue1234",
		"ключ: AKIAIOSFODNN7EXAMPLE1234",
		"contraseña: supersecretvalue1234",
		"senha: supersecretvalue1234",
		"passwort: supersecretvalue1234",
		"密码: supersecretvalue1234",
		"パスワード: supersecretvalue1234",
		"비밀번호: supersecretvalue1234",
	}
	for _, in := range secrets {
		out, _ := Text(in)
		if out == in {
			t.Errorf("not redacted: %q", in)
			continue
		}
		if !strings.Contains(out, "[redacted:") {
			t.Errorf("no marker in %q -> %q", in, out)
		}
	}
}

// The looseness has to stop at prose: these name a key word but assign nothing
// secret, and redacting them would eat ordinary sentences.
func TestOrdinaryProseWithSecretWordsIsUntouched(t *testing.T) {
	prose := []string{
		"пароль от вина был простой",
		"ключевые слова: обычный текст без секретов",
		"the token is fine here",
		"мы обсудили токен и решили его не менять",
	}
	for _, in := range prose {
		if out, _ := Text(in); out != in {
			t.Errorf("prose was redacted: %q -> %q", in, out)
		}
	}
}
