package redact

import (
	"strings"
	"testing"
)

// RE2 has no `\b` for these alphabets, and the key words live inside ordinary
// ones: "включены", "исключение", "переключены" and "выключен" all contain
// "ключ". Unbounded, the filler pattern reached across the sentence and masked
// whatever followed — a markdown link came back as `https:[redacted:credential]`
// and a log line lost its path (#3589).
func TestAWordThatMerelyContainsAKeyWordIsNotAKeyWord(t *testing.T) {
	for _, line := range []string{
		"Discussions включены](https://github.com/vshulcz/deja-vu)",
		"логи включены: /var/log/app/server-2026-09-01.log",
		"исключение: java.lang.IllegalStateException",
		"переключены на новый: some-ordinary-identifier-2026",
		"выключен режим: debug-mode-verbose-2026",
		"ключевые слова: alpha-beta-gamma-delta-epsilon",
		"расключение: /path/to/something/long/enough",
	} {
		if got, _ := Text(line); got != line {
			t.Errorf("an ordinary word was read as a key word: %q -> %q", line, got)
		}
	}
}

// And the key words themselves still work, on their own and with the words
// people put between them and the colon.
func TestTheKeyWordsThemselvesStillFire(t *testing.T) {
	for _, line := range []string{
		"ключ: SomeRealSecretValue2026",
		"пароль: AsciiPassword2026xyz",
		"пароль от стейджа: StagePassword2026x",
		"токен для бота: BotTokenValue2026xx",
		"секрет: SecretValue2026abcdef",
	} {
		got, counts := Text(line)
		if !strings.Contains(got, "[redacted") {
			t.Errorf("a named secret survived: %q -> %q", line, got)
		}
		if counts["credential"] == 0 {
			t.Errorf("nothing counted for %q", line)
		}
	}
}
