package redact

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The finders must return what the regexes return, match for match. These
// tests hold them against the plain regexes on generated text dense with the
// shapes every rule looks for, their near misses, case and Unicode variants,
// and the runes whose case folding changes their width.

var genKeys = []string{
	"password", "PASSWORD", "Passwd", "pwd", "PassPhrase", "secret", "SeCrEt", "token", "TOKEN",
	"api_key", "api-key", "apikey", "API KEY", "x-api-key", "authorization", "Authorization",
	"OPENAI_API_KEY", "DEJA_EMBED_KEY", "GROQ_KEY", "db_pass", "DB.PASS", "my-pass", "bypass",
	"compass", "PGPASSWORD", "aws_secret_access_key",
	"пароль", "ПАРОЛЬ", "пароля", "токен", "Токена", "секрет", "ключ", "ключом", "включены",
	"contraseña", "CONTRASEÑA", "senha", "passwort", "Passwort", "密码", "密碼", "パスワード", "비밀번호",
	"ſecret", "paſſword", "toKen", "api_Key", "ᲂ", "пᲂроль",
	"--password", "-password", "--token", "--api-key", "--secret", "--client_secret", "--passphrase",
	"-pwd", "--auth-token", "--access_token",
	"Bearer", "bearer", "Basic", "Authorization: Token",
	"login", "redis-cli", "ftp", "sftp", "smbclient", "docker login", "machine host login",
	"mysql", "sshpass", "Cookie:", "set-cookie:",
}

var genGlue = []string{
	"=", ":", " = ", ": ", "\\\": \\\"", "\": \"", "='", " is ", " was ", " for ", " IS ",
	" это ", " будет ", " от стейджа: ", " ", "  ", "\t", "\n", ":\n", "=\n  ", "\\\\\"", "'",
	"`", "\"", " -p ", " -a ", " --password=", " -u ", " --user=", " -p", "[", "]", "；", "：",
}

var genValues = []string{
	"hunter2", "S3cr3tP@ssw0rd!", "Sup3rS3cretValue!!xyz", "abcdefghijklmnopqrstuvwxyz012345",
	"9f2b7c1e4a6d8b0f3e5c7a9d1b3f5e7c9a2b4d6f8", "MyRealPass2026", "БазаПароль2026", "非常に長いパスワード2026",
	"неправильный", "$DEPLOY_PASS", "<your-password>", "null", "[redacted:credential]", "1000:1000",
	"ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8", "github_pat_11ABCDEFG0123456789_abcdefghijkl",
	"sk-proj-abcDEF123456789012345678901234-O1pQ_tail", "sk-ant-api03-abcdefghijklmnopqrstuvwx",
	"sk_live_abcdefghijklmnop1234", "rk_test_ABCDEFGHIJKLMNOPQRST", "gsk_abcdefghijklmnopqrstuvwxyz",
	"xai-abcdefghijklmnopqrstuvwxyz", "hf_abcdefghijklmnopqrstuvwxyz", "npm_abcdefghijklmnopqrstuvwxyz0123456789",
	"xoxb-1234567890-abcdefghij", "AIzaSyA1234567890abcdefghijklmnopqrstu", "glpat-abcdefghijklmnopqrstu",
	"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijk", "123456789:AAEabcdefghijklmnopqrstuvwxyz012345678",
	"AKIAABCDEFGHIJKLMNOP", "short", "a", "", "K", "ſ", "x\u212ay", "Ω", "é", "日本語のテキスト2026",
	"MIIEowIBAAKCAQEA7x", "aGVsbG8gd29ybGQgb2YgZGVqYQ==", "tok_with-dash.and/slash+plus=",
}

var genURLs = []string{
	"postgres://app:S3cretPass@db.internal:5432/prod", "https://user:p@host@", "https://x.y://a:b@c",
	"git+ssh://git:tok@github.com/x", "https://example.com/a:b", "mongodb+srv://u:p@cluster0",
	"://:x@y", "https://hooks.slack.com/services/T000/B000/abcdefghijklmnopqrstuvwx",
	"redis://:pass@localhost", "a1-b.c://u:v@w", "http://日本:パス@host",
}

var genNoise = []string{
	"the handler returns 500 when the body is empty", "Discussions включены](https://example.com/a)",
	"исключение", "token лежит в файле: строка 12", "passwords are hard", "the password is wrong",
	"the password is correct2026!", "use the Authorization header", "ключи: ", "\x1b[33m", "\x1b[0m",
	"—", "…", "🙂", "\u00a0", "\r\n", "_", ".", "-", "/", "\\", "@", "#", "(", ")", "&", "?", ",",
}

func genText(r *rand.Rand) string {
	var b strings.Builder
	n := 1 + r.Intn(14)
	for range n {
		switch r.Intn(7) {
		case 0, 1:
			b.WriteString(genKeys[r.Intn(len(genKeys))])
			b.WriteString(genGlue[r.Intn(len(genGlue))])
			if r.Intn(3) == 0 {
				b.WriteString(genGlue[r.Intn(len(genGlue))])
			}
			b.WriteString(genValues[r.Intn(len(genValues))])
		case 2:
			b.WriteString(genURLs[r.Intn(len(genURLs))])
		case 3:
			b.WriteString(genNoise[r.Intn(len(genNoise))])
		case 4:
			b.WriteString(genValues[r.Intn(len(genValues))])
		case 5:
			b.WriteString(genKeys[r.Intn(len(genKeys))])
		case 6:
			// raw bytes, sometimes invalid UTF-8
			k := r.Intn(6)
			for range k {
				b.WriteByte(byte(r.Intn(256))) //nolint:gosec // Intn(256) fits a byte
			}
		}
		if r.Intn(3) == 0 {
			b.WriteString(genGlue[r.Intn(len(genGlue))])
		} else {
			b.WriteByte(" \n\t,;"[r.Intn(5)])
		}
	}
	return b.String()
}

func sameAsRegex(t *testing.T, in string) {
	t.Helper()
	for re, f := range finders {
		want := re.FindAllStringSubmatchIndex(in, -1)
		v := &view{}
		v.reset(in)
		got := f.all(v)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s\n in: %q\nwant %v\n got %v", re, in, want, got)
		}
	}
	wo, wc := text(in, false)
	gs, gc := text(in, true)
	if wo != gs || !reflect.DeepEqual(wc, gc) {
		t.Fatalf("Text differs\n in: %q\nwant %q %v\n got %q %v", in, wo, wc, gs, gc)
	}
}

func TestFindersMatchRegexes(t *testing.T) {
	n := 60000
	if testing.Short() {
		n = 5000
	} else if raceEnabled {
		n = 8000
	}
	r := rand.New(rand.NewSource(1)) //nolint:gosec // seeded so a failure reproduces
	for range n {
		sameAsRegex(t, genText(r))
	}
}

// One long text, the shape a tool output takes, with secrets far apart.
func TestFindersMatchRegexesOnHugeText(t *testing.T) {
	r := rand.New(rand.NewSource(2)) //nolint:gosec // seeded so a failure reproduces
	var b strings.Builder
	for b.Len() < 400<<10 {
		b.WriteString(genText(r))
		b.WriteString(strings.Repeat("ordinary output line with a token and a pass word\n", r.Intn(40)))
	}
	sameAsRegex(t, b.String())
	sameAsRegex(t, strings.Repeat("a", 70)+"_password=Sup3rS3cretValue!!xyz "+strings.Repeat("b.", 40)+"token: abcdefghijklmnopqrstuvwxyz")
}

func FuzzFindersMatchRegexes(f *testing.F) {
	r := rand.New(rand.NewSource(3)) //nolint:gosec // seeded so a failure reproduces
	for range 64 {
		f.Add(genText(r))
	}
	f.Fuzz(func(t *testing.T, in string) { sameAsRegex(t, in) })
}
