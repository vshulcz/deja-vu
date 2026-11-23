// Package redact masks secrets in transcript text before it is indexed or shown.
package redact

import (
	"sync/atomic"
	"unicode/utf8"

	"os"
	"regexp"
	"strings"
)

// Counts is how many matches each kind of secret had.
type Counts map[string]int

// Add counts n more of kind, ignoring n <= 0.
func (c Counts) Add(kind string, n int) {
	if n > 0 {
		c[kind] += n
	}
}

// Total is the sum over every kind.
func (c Counts) Total() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

// kvValue is the value the key-value rules mask. A password often ends in
// punctuation the base class stops at, and `--password=Sup3rS3cretValue!!xyz`
// came back as `[redacted:credential]!!xyz`: the tail sat in the index in the
// clear. Runs of those characters and what follows them are taken in too. '&',
// '?', ',' and ')' stay out, so a query string or a sentence keeps its rest.
const kvValue = `[A-Za-z0-9/+=._-]{16,}(?:[!#$%^*~@]+[A-Za-z0-9/+=._-]*)*`

var (
	awsAccessKeyRE = regexp.MustCompile(`A(?:KIA|SIA)[0-9A-Z]{16}`)
	awsSecretRE    = regexp.MustCompile(`(?i)\b(aws[_-]?secret[_-]?access[_-]?key)(\\*['"]?\s*[:=]\s*)(\\*['"]?)([A-Za-z0-9/+=_-]{32,})(\\*['"]?)`)
	// The key may be embedded in a larger identifier (ANTHROPIC_API_KEY,
	// x-api-key) and, in JSON, a closing quote can sit between the key and the
	// delimiter ("api_key": "..."). Tolerate both so env-var and JSON forms are
	// caught, not just a bare `api_key=`.
	// The key words are matched in the languages people actually type in, not
	// only English: a Russian speaker writes "пароль: …" or "токен: …" and the
	// secret sat in the clear because every pattern here was English-only. The
	// value class and length floor are the same, so the looseness is unchanged.
	genericKVRE = regexp.MustCompile(`(?i)\b([\w.-]{0,64}?(?:api[_-]?key|secret|token|passwd|password|authorization))(\\*['"]?\s*[:=]\s*)(\\*['"]?)(` + kvValue + `)(\\*['"]?)`)
	// An environment variable holding a credential does not have to say "api" or
	// "token" in its name: DEJA_EMBED_KEY, GROQ_KEY, VOYAGE_KEY all end in plain
	// _KEY, which genericKVRE never matched, so an opaque value — one with no
	// provider prefix and not enough entropy for the last-resort pass — was
	// indexed in the clear. Measured on `export DEJA_EMBED_KEY=9f2b…f83`.
	//
	// Case-sensitive on purpose: this is the shell shape, and matching `_key`
	// as well would take `cache_key: <16 chars>` out of every YAML file people
	// paste, which costs recall for no secret.
	envKeyRE = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*_KEY)(\\*['"]?\s*[:=]\s*)(\\*['"]?)(` + kvValue + `)(\\*['"]?)`)
	// The same shape in the languages people actually type in. \b is ASCII-only
	// in RE2, so a Cyrillic or CJK key word can never sit behind it — these get
	// their own pattern. A Russian speaker writing "пароль: …" had the secret
	// stored in the clear because every pattern here was English-only.
	genericKVIntlRE = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}_])(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호)(\\*['"]?\s*[:=]\s*)(\\*['"]?)(` + kvValue + `)(\\*['"]?)`)
	// The same key words with a few of their own between the word and the
	// colon. "пароль от стейджа: …" is how the line is actually written, and
	// genericKVIntlRE needs the delimiter to follow the word directly. The
	// value class is unchanged — 16 or more ASCII characters — so a sentence
	// that merely mentions a token ("токен лежит в файле: строка 12") still
	// matches nothing.
	// Bounded on both sides, because RE2 has no \b for these alphabets and the
	// key words live inside ordinary ones: "включены", "исключение",
	// "переключены" and "выключен" all contain "ключ". Unbounded, the filler
	// reached across the sentence and masked what followed — a markdown link
	// came back as `https:[redacted:credential]` and a log path as the whole
	// value (#3589). The leading group is a character, not a lookbehind, so it
	// is put back with the rest.
	//
	// The filler stops at '[': an earlier rule's marker carries a colon of its
	// own, so "токен [redacted:telegram-bot-token]" read as a key word, some
	// filler, a delimiter and an eighteen-character value, and came out as
	// `[redacted:[redacted:credential]]`.
	genericKVIntlFillerRE = regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}_])(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호)([^\p{L}\n:=\[][^\n:=\[]{0,32}[:=]\s*)(\\*['"]?)(` + kvValue + `)(\\*['"]?)`)
	bearerRE              = regexp.MustCompile(`(?i)\b(Bearer|Basic)(\s+)([A-Za-z0-9._~+/=-]{16,})`)
	// The `Token` scheme (Django REST framework, GitHub's legacy `token`). Only
	// behind an Authorization header: "token" alone is an ordinary word.
	authTokenRE = regexp.MustCompile(`(?i)\b(authorization\\*["']?\s*[:=]\s*\\*["']?\s*token)(\s+)([A-Za-z0-9._~+/=-]{16,})`)
	// A webhook URL is a credential whole: whoever holds it can post. The path
	// ids stay so the line still says which hook it was. The host is checked
	// beside the call, so the pattern is the path alone.
	webhookPathRE = regexp.MustCompile(`(/(?:services|workflows|triggers)/[A-Za-z0-9]+/[A-Za-z0-9]+/|/api/webhooks/[0-9]+/)([A-Za-z0-9_-]{16,})`)
	// A secret named in prose and quoted rather than assigned. Tool output is
	// full of this shape — `password authentication failed for user "admin"
	// with password "S3cr3tP@ssw0rd!"` — and genericKVRE cannot reach it:
	// there is no `=` or `:`, the value carries punctuation its class excludes,
	// and at 15 characters it is under the 16-char floor. Measured: five of six
	// planted secret forms were redacted at ingest and this one sat in
	// records.bin in the clear, visible through `deja show`.
	//
	// The quotes are what make it safe to be this loose: "password
	// authentication failed" has no quoted value and matches nothing.
	quotedSecretRE = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[\s_-]?key)(\s+(?:is\s+|was\s+|for\s+)?)(\\*["'` + "`" + `])([^"'` + "`" + `\n]{6,80})(\\*["'` + "`" + `])`)
	// The same names in the shape a header or a config line is written in, for
	// a value too short for genericKVRE's class: `x-api-key: "s3cretvalue"`
	// went through in the clear, because the prose rule wants whitespace after
	// the name and the KV rule wants sixteen characters of value (#3614).
	quotedAssignedSecretRE = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[\s_-]?key)(\\*['"]?\s*[:=]\s*)(\\*["'` + "`" + `])([^"'` + "`" + `\n]{6,80})(\\*["'` + "`" + `])`)
	pemPrivateRE           = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----.*?-----END [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----`)
	// A key pasted into a transcript is often not pasted whole: the output was
	// truncated, the session ended, the tail landed in another message. The
	// closing marker was required, so the half that carries the key material
	// was the half that went through (#2409). This runs after the whole-block
	// pattern and takes the header plus the base64 lines under it — the lines,
	// not the rest of the message, so the prose around a key survives.
	// At least one body line: a bare header carries nothing, and eating it
	// alone would hide the marker that lets the whole-block pattern pair it
	// with a body that arrives in the next field.
	//
	// A line break here is a real one or the two characters `\n` of a key
	// printed inside JSON, a body line may be indented (a YAML block scalar),
	// and armour headers (`Comment: …`) and the blank line after them may sit
	// between the header and the body, as they do in a PGP key.
	pemPrivateOpenRE = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----` +
		`(?:` + pemBreak + `[A-Za-z][A-Za-z-]*: [^\r\n\\]*)*(?:` + pemBreak + `)?` +
		`(?:` + pemBreak + `[A-Za-z0-9+/=]{16,}[ \t]*)+`)
	// Provider prefixes. sk- allows internal hyphens/underscores so modern
	// hyphenated formats (sk-ant-…, sk-proj-…) are covered, not just legacy
	// sk-<alnum> keys. xai- stays alphanumeric-only: real xAI keys have no
	// internal hyphens, and allowing them makes every long kebab-case slug
	// that happens to start with "xai-" (branch names, doc titles) a false
	// positive.
	//
	// The sk- form reads on past its last long run: `_` is a word character,
	// so a key whose tail held one stopped at the last `-` before it, and the
	// rest of the key went through (sk-proj-…-O1pQ…_tail).
	providerRE = regexp.MustCompile(`\b(gh[opsur]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}|sk-[A-Za-z0-9_-]*[A-Za-z0-9]{20,}[A-Za-z0-9_-]*|gsk_[A-Za-z0-9]{20,}|xai-[A-Za-z0-9]{20,}|hf_[A-Za-z0-9]{20,}|npm_[A-Za-z0-9]{30,}|xox[bpcs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})\b`)
	jwtRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\b`)
	// A Telegram bot token: the bot's numeric id, a colon, and 35 characters
	// that open with "A". It has no prefix providerRE could key on, and the
	// colon in the middle defeats every assignment rule — `BOT_TOKEN=<id>:<…>`
	// hands genericKVRE a value that stops at the colon, nine digits long — so
	// the only thing that ever caught one was the entropy pass, and only when a
	// label sat in front of it. "вот токен <value>" and a `NAME=` line went
	// into the index as written.
	//
	// The leading group is a non-digit rather than \b so the token is found
	// where it most often travels, inside `api.telegram.org/bot<id>:<…>`. The
	// tail is read greedily and the length checked after, because RE2 has no
	// lookahead: a run longer than 35 is some other identifier.
	telegramBotTokenRE = regexp.MustCompile(`(?:^|[^0-9])([0-9]{5,16}:A[A-Za-z0-9_-]{34,})`)
	// Password is greedy so a password containing '@' (user:p@ss@host) splits on
	// the last '@' and is redacted whole, not just up to the first '@'.
	connURLRE = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://)([^\s/@:]*):([^\s]+)@([^\s]+)`) // scheme://[user]:pass@host
	// A credential handed to a program as an argument. Nothing above reaches
	// these: `sshpass -p hunter2` has no key word beside the value, and
	// `curl -u admin:hunter2` is a pair connURLRE only sees inside a URL.
	// Measured on twelve planted shapes — ten were redacted at ingest and
	// these two reached `deja show`, `deja recall` and `deja sync export` in
	// the clear.
	//
	// Each pattern names the command or the flag, so the value's own shape
	// does not have to carry the decision: a password is short, punctuated and
	// indistinguishable from a word, which is why the length floors here are
	// well under the 16 the key-value patterns need.
	sshpassRE = regexp.MustCompile(`(?i)\bsshpass\b[^\n]{0,64}?-p[ =]?['"]?([^\s'"]{3,})`)
	// Both halves of `-u user:pass`. `docker run -u 1000:1000` is a uid and a
	// gid in the same shape, so an all-numeric pair is left alone below.
	userPairRE = regexp.MustCompile(`(?i)(?:^|\s)(?:-u|--user)[ =]['"]?([^\s:'"]{1,64}):([^\s'"]{3,})`)
	// `mysql -pSecret` attaches the value to the flag, which is the one form
	// that cannot be confused with ssh's `-p <port>`.
	mysqlPassRE = regexp.MustCompile(`(?i)\b(?:mysql|mysqladmin|mysqldump|mariadb)\b[^\n]{0,120}?\s-p([^\s'"-]\S{2,})`)
	// `docker login -p …`, `az login -p …`, `redis-cli -a …`. The command gate
	// is what keeps `-p` apart from a port: ssh, scp and `docker run -p` all
	// spell one the same way, and a numeric value is left alone below.
	loginPassRE = regexp.MustCompile(`(?i)\b(?:login|redis-cli|ftp|smbclient)\b[^\n]{0,120}?\s(?:-p|-a|--password|--with-token)[ =<]{1,6}['"]?([^\s'"]{3,})`)
	// A .netrc line: `machine api.example.com login deploy password hunter2`.
	// The value follows the key word with no delimiter at all, so every
	// key-value pattern here missed it, and `password` on its own is the
	// opening of far more prose than credentials — "password authentication
	// failed" — which is why the machine or login word before it is required.
	netrcRE = regexp.MustCompile(`(?i)\b(?:machine|login)\s+\S+[^\n]{0,120}?\b(?:password|passwd)\s+([^\s'"]{3,})`)
	// A cookie header. Its value is a credential whole, and "session" is too
	// ordinary a word to put in the key-value list — deja's own commands are
	// full of `--session <id>`, and redacting those would cost the recall they
	// exist for. The header is what makes this one unambiguous.
	//
	// Stops at '[' as well as a newline: inside JSON-escaped text a line break
	// is the two characters `\n`, so a second pass over `Cookie: [redacted:cookie]`
	// without `[` in the stop set swallowed the marker and the next 383 bytes
	// after it (#5763).
	cookieRE = regexp.MustCompile(`(?i)\b(?:set-)?cookie:[ \t]*([^\n[]{8,400})`)
	// A password named as one, at the length people actually choose. The
	// key-value patterns above take any value of sixteen characters or more,
	// which is right for `token = …` where a short value is as likely to be a
	// word — and wrong here, because the key says what the value is. Measured
	// through an index pass: `mysql --password=MyRootPass2026` (fourteen) and
	// `app --password=Pass2026Short` (thirteen) reached `deja show` and `deja
	// share` in the clear, while the same line with a nineteen-character value
	// was redacted (#3572).
	//
	// Only the flag form. `password: hunter2` stays a near miss on purpose —
	// the bare word opens more prose than credentials, and a floor low enough
	// to catch a short one there would redact sentences (escaped_json_test).
	//
	// The names beyond `password` are the ones a transcript hands a real secret
	// to: `gpg --passphrase`, `borg --passphrase`, and the `--secret`,
	// `--token` and `--api-key` every CLI with an account behind it takes. They
	// arrived with the guard below, not before it: `--token` and `--secret` are
	// ordinary words in a sentence about a flag, which `--password` is not, and
	// widening the list without the guard would turn a rare wrong mask into a
	// common one (#3596). Single-letter flags stay out — `-p` is a port as
	// often as a password, and telling them apart needs the command.
	passwordFlagRE = regexp.MustCompile(`(?i)(^|\s)(--?(?:password|passwd|pwd|passphrase|secret|token|api[_-]?key|auth[_-]?token|access[_-]?token|client[_-]?secret))([ =]\\*['"]?)([^\s'"]{3,128})`)
	// A password assigned with `=`, at the length people actually choose. The
	// key-value floor of sixteen characters is right where the key word could
	// be describing anything — `token`, `secret`, `key` — and wrong for this
	// family, where the word names the value: measured through an index pass,
	// `password=JdbcPass2026` in a JDBC URL, `password=QueryPass2026` in a
	// query string, `--from-literal=password=K8sPass2026x` and a dotenv
	// `DATABASE_PASSWORD=DotenvPass2026` all reached `deja show` in the clear
	// (#3588).
	//
	// An equals sign and not a colon. `password: hunter2` is prose by an older
	// decision this does not touch (escaped_json_test) — a colon is how a
	// sentence is written and `=` is how a value is assigned.
	passwordAssignRE = regexp.MustCompile(`(?i)\b([\w.-]{0,64}?(?:password|passwd|pwd))(\\*['"]?=\s*)(\\*['"]?)([^\s'"&]{3,128})`)
	// `DB_PASS=…`, the other half of the same family. Its own pattern because
	// `pass` is a word that lives inside others: the separator before it is
	// what keeps this out of `bypass=` and `compass=`, and an env var is
	// `DB_PASS`, never `dbpass`. The full words above need no such guard —
	// `PGPASSWORD` has no separator and is a credential all the same.
	passSuffixAssignRE = regexp.MustCompile(`(?i)(?:^|[^\w.-])([\w.-]*[_.\-]pass)(\\*['"]?=\s*)(\\*['"]?)([^\s'"&]{3,128})`)
	// A secret whose VALUE is not ASCII. Every pattern above ends in
	// `[A-Za-z0-9/+=._-]{16,}`, so `пароль: БазаПароль2026` and `password:
	// 非常に長いパスワード2026` were stored in the clear whatever the key word or
	// the length — #1319 widened the key words to the languages people type in
	// and left the value class where it was (#3587).
	//
	// The digit is what keeps this away from prose. A value class wide enough
	// for Cyrillic is wide enough for an ordinary word, and "пароль:
	// неправильный" is a sentence; a passphrase someone chose is
	// "БазаПароль2026". Eight runes rather than sixteen because a word in
	// these scripts carries more meaning per character — `数据库密码2026年` is
	// ten — the same reasoning #1319 recorded for the line-length bound.
	//
	// Words between the key and the colon, the way genericKVIntlFillerRE
	// allows: "пароль от стейджа: …" is how the line is actually written.
	// Stops at '[' as well as a newline so a provenance tag (`[claude:id/user]`)
	// whose truncated id carries `…` is not taken as the value of a key word
	// earlier on the line.
	intlValueRE = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호|api[_-]?key|secret|token|passwd|password)([^\p{L}\n:=\[]?[^\n:=\[]{0,32}[:=]\s*)(\\*['"]?)([^\s'"]*[^\x00-\x7f][^\s'"]*)(\\*['"]?)`)
	// A credential stated in prose rather than assigned. Every pattern above
	// wants a delimiter — a colon, an equals sign, a flag — and a person
	// telling an agent a password does not use one: "also the admin password is
	// hunter2-2026, do not put it in code" went into the index verbatim, so
	// `deja show` and `deja ctx` read it back in the clear. Found by seeding
	// fake credentials into a transcript and asking every surface for them
	// (#3729).
	//
	// The digit or the symbol in the value is what keeps this away from prose,
	// the rule worthRedactingIntl already rests on: "the password is wrong",
	// "the password is correct" and "the password is the same as staging" carry
	// neither, and the value class stops at the first space so a sentence
	// cannot be swallowed.
	prosePasswordRE = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(password|passphrase|парол[ьяею])(\s+(?:is|was|это|будет)\s+)(\\*['"]?)([^\s'"]{8,128})`)
)

// worthRedactingIntl reports whether a non-ASCII value looks like a secret
// rather than a word. A digit is the signal: prose in these scripts does not
// carry one, and a passphrase someone chose usually does.
func worthRedactingIntl(v string) bool {
	v = strings.Trim(v, `"'`)
	if n := utf8.RuneCountInString(v); n < 8 || n > 128 {
		return false
	}
	if notASecretValue(v) {
		return false
	}
	for _, r := range v {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// hasNonASCII reports whether the text carries a byte outside ASCII, which is
// the necessary condition for intlValueRE to match anything.
func hasNonASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// notASecretValue reports whether what follows a password key is plainly not a
// password: a placeholder, a variable the shell will expand, or a word that
// says there is none. Redacting those costs the reader the sentence and hides
// nothing — `password: null` is a fact about a config, not a credential.
func notASecretValue(v string) bool {
	v = strings.Trim(v, `"'`)
	if v == "" {
		return true
	}
	switch v[0] {
	case '$', '<', '%', '{', '*':
		return true
	}
	switch strings.ToLower(v) {
	case "true", "false", "null", "none", "nil", "empty", "unset", "changeme", "password", "redacted":
		return true
	}
	return strings.HasPrefix(v, "[redacted")
}

// chosenSecret reports whether a value someone stated in prose looks like a
// credential they chose rather than the next word of the sentence. A digit or
// a symbol is the signal — the same one worthRedactingIntl uses — and a value
// that is only letters is a word.
func chosenSecret(v string) bool {
	v = strings.Trim(v, `"'`)
	if n := utf8.RuneCountInString(v); n < 8 || n > 128 {
		return false
	}
	for _, r := range v {
		if r >= '0' && r <= '9' {
			return true
		}
		switch r {
		case '!', '@', '#', '$', '%', '^', '&', '*', '_', '-', '+', '=', '.', '/', '\\', '|', '~', ':':
			return true
		}
	}
	return false
}

// flagHintNearby is the cheap necessary condition for passwordFlagRE: one of
// the flag names, with the dash that makes it a flag. Every alternative in the
// pattern begins with one of these once the leading dash is counted, so a text
// that holds none of them cannot match.
func flagHintNearby(lower string) bool {
	for _, hint := range flagHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

var flagHints = []string{"-passw", "-pwd", "-passphrase", "-secret", "-token", "-api", "-auth", "-access", "-client"}

// proseWordAfterFlag reports whether what follows `--flag ` is a word rather
// than a value. Only the space-separated form asks: `--password=X` is a shell
// assignment and X is the value whatever it looks like, while `run with
// --password from the keychain` is a sentence, and it lost "from" to the
// redactor (#3596).
//
// A word here is all lowercase ASCII letters and hyphens, no longer than
// twelve characters — which also catches the next flag, `--verbose`. What it
// costs is a real password of twelve lowercase letters or fewer handed over
// with a space; that is the same trade `password: hunter2` already makes, and
// nothing shorter than a word can be told from one.
//
// It does not spare a digit, so `docker service create --secret my-secret-v2`
// masks a secret's *name*. That is a line of recall lost rather than a secret
// shown, and the alternative — letting a lowercase value with a digit through —
// is exactly `hunter2`, which is what this pattern exists to catch.
func proseWordAfterFlag(sep, v string) bool {
	if !strings.Contains(sep, " ") {
		return false
	}
	v = strings.Trim(v, `"'`)
	if len(v) > 12 {
		return false
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	return v != ""
}

// Disabled reports whether DEJA_NO_REDACT=1 turned redaction off.
func Disabled() bool { return os.Getenv("DEJA_NO_REDACT") == "1" }

// kvAssignmentNearby reports whether any key-ish word in the text is followed
// by an assignment. genericKVRE cannot match unless one is, so this is a
// necessary condition and skipping on a negative cannot change what is
// redacted — TestKVGateNeverHidesAMatch proves it on random input.
//
// The word gate alone was not enough. "token", "secret" and "key" are ordinary
// words in a developer's session, so the gate passed constantly and the regex
// ran over the whole message to find nothing: on a 1.6 KB message of ordinary
// chatter it cost 289µs, which was the single largest item in index-time
// redaction.
func kvAssignmentNearby(lower string) bool {
	return kvAssignmentNearbyHints(lower, kvHints)
}

// kvAssignmentNearbyHints is kvAssignmentNearby for one pattern's own words.
func kvAssignmentNearbyHints(lower string, hints []string) bool {
	for _, hint := range hints {
		for at := 0; ; {
			i := strings.Index(lower[at:], hint)
			if i < 0 {
				break
			}
			pos := at + i + len(hint)
			if assignmentFollows(lower, pos) {
				return true
			}
			at += i + 1
		}
	}
	return false
}

// assignmentFollows skips what genericKVRE allows between the name and the
// value — the rest of a longer name, spaces and an optional quote — and looks
// for the ':' or '=' the pattern requires.
func assignmentFollows(s string, i int) bool {
	// Bytes >= 0x80 continue a non-ASCII word: the hint "парол" stops one byte
	// short of "пароля", and skipping only ASCII left the ':' unreachable, so
	// the gate said no and the intl pattern never ran.
	for i < len(s) && (isWordByte(s[i]) || s[i] >= 0x80 || s[i] == '.' || s[i] == '-') {
		i++
	}
	// The pattern uses \s, which is more than a space: a fuzz case of
	// "passwd\n:" slipped past an earlier version of this that only skipped
	// spaces and tabs.
	// The backslash is here for the same reason the quote is: agents paste
	// nested JSON, where every quote arrives escaped, and `api_key\":` never
	// reached the pattern because the gate stopped at the backslash (#1765).
	for i < len(s) && (isSpaceByte(s[i]) || s[i] == '\'' || s[i] == '"' || s[i] == '\\') {
		i++
	}
	return i < len(s) && (s[i] == ':' || s[i] == '=')
}

func isSpaceByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

// kvHints are the substrings genericKVRE can anchor on; providerHints the
// literal prefixes of providerRE. Checking them first keeps the regexes off
// the vast majority of messages, which contain no credentials at all.
// The non-English words are here for the same reason they are in genericKVRE:
// the gate runs first, so a Russian "пароль:" never reached the regex at all.
var kvHints = []string{
	"key", "secret", "token", "passw", "authorization",
	"пароль", "парол", "токен", "секрет", "ключ",
	"contraseña", "senha", "mot de passe", "passwort",
	"密码", "密碼", "パスワード", "비밀번호",
}

// intlPatternRuns counts how often the international pattern is actually run.
// The gate below cannot be checked by what comes back — text the pattern cannot
// match reads the same whether it ran or not — and the whole point of the gate
// is that it does not run, so the count is what a test can hold on to.
var intlPatternRuns atomic.Int64

// kvIntlHints are the words genericKVIntlRE itself is written from. It was
// gated on the whole list above, so an English transcript that says "token"
// ran a case-insensitive alternation of Cyrillic, Chinese, Japanese and Korean
// words over every message — measured over 6.1 MB of real transcript text,
// 2227 ms of the 4523 ms redaction spends, and the pattern cannot match a byte
// of it.
var kvIntlHints = []string{
	"пароль", "парол", "токен", "секрет", "ключ",
	"contraseña", "senha", "passwort",
	"密码", "密碼", "パスワード", "비밀번호",
}

// Each hint is a literal the regex cannot match without, so skipping on a
// negative cannot change what is redacted. They have to be as long as the
// literal prefix actually is: "gh" alone is inside "through", "right" and
// "enough", so it let ordinary English through the gate and put the provider
// regex on nearly every message in a chat history — a third of index build
// time spent proving that "thought" is not a GitHub token. The regex wants
// gh[opsur]_, so the hints spell that out. "github_pat_" is separate because
// "gh" is not a substring of "github".
var providerHints = []string{
	"ghp_", "gho_", "ghs_", "ghu_", "ghr_", "github_pat_",
	"glpat-", "sk_", "rk_", "sk-", "gsk_", "xai-", "hf_", "npm_", "xox", "AIza",
}

func containsAnyFold(s string, hints []string) bool {
	for _, h := range hints {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}

func isRegexpWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// asciiLower lowercases ASCII bytes without walking UTF-8 runes when only ASCII
// delimiters matter, falling back to strings.ToLower if U+0130 or U+212A appear.
func asciiLower(s string) string {
	var b strings.Builder
	grown := false
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if !grown {
				b.Grow(len(s))
				grown = true
			}
			b.WriteString(s[last:i])
			b.WriteByte(c + ('a' - 'A'))
			last = i + 1
			continue
		}
		if c == 0xc4 && i+1 < len(s) && s[i+1] == 0xb0 {
			return strings.ToLower(s)
		}
		if c == 0xe2 && i+2 < len(s) && s[i+1] == 0x84 && s[i+2] == 0xaa {
			return strings.ToLower(s)
		}
	}
	if !grown {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// containsAnchored searches s for sub by scanning for sub[anchor], which lets
// callers anchor on a rarer byte than sub[0].
func containsAnchored(s, sub string, anchor int) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	tail := len(sub) - anchor
	target := sub[anchor]
	for i := anchor; i+tail <= len(s); {
		j := strings.IndexByte(s[i:len(s)-tail+1], target)
		if j < 0 {
			return false
		}
		pos := i + j
		start := pos - anchor
		if s[start:start+len(sub)] == sub {
			return true
		}
		i = pos + 1
	}
	return false
}

func hasAWSSecretHint(lower string) bool {
	for i := 1; i+1 < len(lower); {
		j := strings.IndexByte(lower[i:len(lower)-1], 'w')
		if j < 0 {
			return false
		}
		k := i + j
		if lower[k-1] == 'a' && (k == 1 || !isRegexpWordByte(lower[k-2])) && lower[k+1] == 's' {
			rest := lower[k+2:]
			if strings.HasPrefix(rest, "_secret") || strings.HasPrefix(rest, "-secret") || strings.HasPrefix(rest, "secret") {
				return true
			}
		}
		i = k + 1
	}
	return false
}

func hasBearerOrBasic(lower string) bool {
	for i := 0; i < len(lower); {
		j := strings.IndexByte(lower[i:], 'b')
		if j < 0 {
			return false
		}
		k := i + j
		if k == 0 || !isRegexpWordByte(lower[k-1]) {
			rest := lower[k:]
			if (len(rest) >= 7 && rest[1:6] == "earer" && isSpaceByte(rest[6])) ||
				(len(rest) >= 6 && rest[1:5] == "asic" && isSpaceByte(rest[5])) {
				return true
			}
		}
		i = k + 1
	}
	return false
}

// hasProviderHint reports whether s contains any provider token prefix from
// providerRE at a word boundary, scanning once per delimiter byte rather than once per prefix.
func hasProviderHint(s string) bool {
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '_')
		if j < 0 {
			break
		}
		k := i + j
		if k >= 2 {
			p2 := s[k-2 : k]
			if (p2 == "sk" || p2 == "rk") && (k == 2 || !isRegexpWordByte(s[k-3])) &&
				(strings.HasPrefix(s[k+1:], "live_") || strings.HasPrefix(s[k+1:], "test_")) {
				return true
			}
			if p2 == "hf" && (k == 2 || !isRegexpWordByte(s[k-3])) {
				return true
			}
			if k >= 3 {
				if s[k-3] == 'g' && s[k-2] == 's' && s[k-1] == 'k' && (k == 3 || !isRegexpWordByte(s[k-4])) {
					return true
				}
				if s[k-3] == 'n' && s[k-2] == 'p' && s[k-1] == 'm' && (k == 3 || !isRegexpWordByte(s[k-4])) {
					return true
				}
				if s[k-3] == 'g' && s[k-2] == 'h' && (k == 3 || !isRegexpWordByte(s[k-4])) {
					switch s[k-1] {
					case 'p', 'o', 's', 'u', 'r':
						return true
					}
				}
				if k >= 10 && s[k-10:k] == "github_pat" && (k == 10 || !isRegexpWordByte(s[k-11])) {
					return true
				}
			}
		}
		i = k + 1
	}
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '-')
		if j < 0 {
			break
		}
		k := i + j
		if k >= 2 {
			if s[k-2] == 's' && s[k-1] == 'k' && (k == 2 || !isRegexpWordByte(s[k-3])) {
				return true
			}
			if k >= 3 && s[k-3] == 'x' && s[k-2] == 'a' && s[k-1] == 'i' && (k == 3 || !isRegexpWordByte(s[k-4])) {
				return true
			}
			if k >= 4 && s[k-4] == 'x' && s[k-3] == 'o' && s[k-2] == 'x' && (k == 4 || !isRegexpWordByte(s[k-5])) {
				switch s[k-1] {
				case 'b', 'p', 'c', 's':
					return true
				}
			}
			if k >= 5 && s[k-5:k] == "glpat" && (k == 5 || !isRegexpWordByte(s[k-6])) {
				return true
			}
		}
		i = k + 1
	}
	for i := 2; i+1 < len(s); {
		j := strings.IndexByte(s[i:len(s)-1], 'z')
		if j < 0 {
			return false
		}
		k := i + j
		if s[k-2] == 'A' && s[k-1] == 'I' && s[k+1] == 'a' && (k == 2 || !isRegexpWordByte(s[k-3])) {
			return true
		}
		i = k + 1
	}
	return false
}

// Text applies every rule.
//
// Colored output splits a secret with terminal escapes
// (`TOKEN\x1b[0m=\x1b[33m<value>`): no rule matched it, and the display pass,
// which strips them, printed the value whole. So text holding an escape is
// read again without them, and that reading wins when it finds more. Text
// with nothing behind its escapes keeps them, as the index keeps a transcript
// as it found it.
func Text(s string) (string, Counts) { return text(s, true) }

// text is Text with the finders on or off; off is the plain regexes, which a
// test holds the finders against.
func text(s string, fast bool) (string, Counts) {
	out, counts := textPass(s, nil, fast)
	if strings.IndexByte(out, 0x1b) < 0 {
		return out, counts
	}
	plain, more := textPass(StripEscapes(out), nil, fast)
	if len(more) == 0 {
		return out, counts
	}
	if counts == nil {
		counts = Counts{}
	}
	for k, n := range more {
		counts.Add(k, n)
	}
	return plain, counts
}

var authHints = []string{"authorization"}

// pemBreak is a line break inside a key block: a real one, or `\n` written
// out inside a JSON string, with the next line's indentation.
const pemBreak = `(?:[ \t]*\r?\n|(?:\\+r)?\\+n)[ \t]*`

// TextKinds applies only the rules named in allow, leaving every other shape as
// written. `deja secrets --scrub` rewrites someone's own transcripts, and it may
// only touch what the report named: the counted rules — `entropy`,
// `credential`, `quoted-secret` — fire on the value side of an assignment as
// often as on a secret, and turning a digest or a variable reference in a
// person's history into a redaction marker is worse than leaving it (#3823).
//
// A nil allow set means every rule, so Text is this function with no filter and
// the two cannot drift apart.
func TextKinds(s string, allow map[string]bool) (string, Counts) { return textPass(s, allow, true) }

func textPass(s string, allow map[string]bool, fast bool) (string, Counts) {
	if Disabled() || s == "" {
		return s, Counts{}
	}
	out, counts := scanPass(s, allow, fast)
	if counts == nil {
		return out, Counts{}
	}
	return out, counts
}

func scanPass(s string, allow map[string]bool, fast bool) (string, Counts) {
	if s == "" {
		return s, nil
	}
	p := kindPass{allow: allow, fast: fast}
	var lower string
	if p.on("credential") {
		lower = strings.ToLower(s)
	} else {
		lower = asciiLower(s)
	}
	s = redactKeyShapes(s, lower, &p)
	s = redactAssignments(s, lower, &p)
	s = redactCommandPasswords(s, lower, &p)
	s = redactEntropy(s, &p)
	return s, p.counts
}

// redactKeyShapes masks the values that say what they are: key blocks, URL
// credentials, provider keys, tokens and JWTs. lower is s lowered before any rule ran.
func redactKeyShapes(s, lower string, p *kindPass) string {
	if p.on("private-key") && strings.Contains(s, "-----BEGIN") {
		s = replaceWhole(s, pemPrivateRE, "private-key", p)
		s = replaceWhole(s, pemPrivateOpenRE, "private-key", p)
	}
	if (p.on("url-credentials") || p.on("webhook-url")) && strings.Contains(s, "://") {
		if p.on("url-credentials") && strings.IndexByte(s, '@') >= 0 {
			s = replaceGroup(s, connURLRE, 3, "url-credentials", p, nil)
		}
		// Under the URL gate: a webhook travels as https://…, and another
		// scan of every message cost more than the rule.
		if p.on("webhook-url") && (strings.Contains(lower, "hooks.slack.com") || strings.Contains(lower, "discord")) {
			s = replaceSubmatch(s, webhookPathRE, "webhook-url", p, func(m []string) string {
				return m[1] + "[redacted:webhook-url]"
			})
		}
	}
	if p.on("aws-secret") && hasAWSSecretHint(lower) {
		s = replaceSubmatch(s, awsSecretRE, "aws-secret", p, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:aws-secret]" + closingQuote(m[3], m[5])
		})
	}
	if p.on("aws-access-key") && (containsAnchored(s, "AKIA", 1) || containsAnchored(s, "ASIA", 2)) {
		s = replaceWhole(s, awsAccessKeyRE, "aws-access-key", p)
	}
	// Before the assignment rules, not after them. A provider prefix says what
	// a value is; sitting next to `token=` only says that something was
	// assigned, and the generic rule got there first — so the commonest way a
	// key appears, `GITHUB_TOKEN=ghp_…`, was masked as `credential`. Measured
	// over the 54,269 windows holding a provider prefix in one machine's
	// transcripts: `credential` fell from 171 to 49 and `bearer-token` from 32
	// to 20, while openai-key rose 137→189, github-token 87→123,
	// anthropic-key 16→36 and stripe-key 22→34. The same values are masked
	// either way; this is about being able to tell someone which key leaked
	// (#536).
	if hasProviderHint(s) {
		s = replaceProvider(s, p)
	}
	if p.on("telegram-bot-token") && strings.Contains(s, ":A") {
		s = replaceGroup(s, telegramBotTokenRE, 1, "telegram-bot-token", p, func(m []string) bool {
			_, tail, _ := strings.Cut(m[1], ":")
			return len(tail) != 35
		})
	}
	// The gate has to admit every spelling the pattern accepts, and it did not:
	// `api_key "…"` and `apikey "…"` were masked while `api-key "…"`,
	// `API-KEY: "…"` and `x-api-key: "…"` went through in the clear, because
	// the hyphen was missing here (#3614).
	//
	// ContainsAny is the cheap half: the pattern needs a quote character, so a
	// text without one cannot match it, and this drops the rule from 1023 runs
	// to 769 on a 4851-message sample (#3491).
	if p.on("quoted-secret") && strings.ContainsAny(s, "\"'`") &&
		(strings.Contains(lower, "password") || strings.Contains(lower, "passwd") ||
			strings.Contains(lower, "pwd") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "token") || strings.Contains(lower, "api key") ||
			strings.Contains(lower, "api-key") || strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey")) {
		s = replaceGroup(s, quotedSecretRE, 4, "quoted-secret", p, nil)
	}
	// The header, not the word: "the Authorization header" is ordinary prose.
	// "uthoriz", not "authoriz": the scan stops on every first byte, and an
	// 'a' is in every other hex digest.
	if p.on("bearer-token") && containsAnchored(lower, "uthoriz", 6) && strings.Contains(lower, "token") && kvAssignmentNearbyHints(lower, authHints) {
		s = replaceSubmatch(s, authTokenRE, "bearer-token", p, func(m []string) string {
			return m[1] + m[2] + "[redacted:bearer-token]"
		})
	}
	if p.on("bearer-token") && hasBearerOrBasic(lower) {
		s = replaceSubmatch(s, bearerRE, "bearer-token", p, func(m []string) string {
			return m[1] + m[2] + "[redacted:bearer-token]"
		})
	}
	if p.on("jwt") && containsAnchored(s, "eyJ", 2) {
		s = replaceWhole(s, jwtRE, "jwt", p)
	}
	return s
}

// redactAssignments masks values assigned to a key word, quoted, flagged or in prose.
func redactAssignments(s, lower string, p *kindPass) string {
	if !p.on("credential") && !p.on("quoted-secret") {
		return s
	}
	if p.on("credential") && kvAssignmentNearby(lower) {
		s = replaceSubmatch(s, genericKVRE, "credential", p, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
		s = replaceSubmatch(s, envKeyRE, "credential", p, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
	}
	// After the KV rules: a value long enough for them keeps the `credential`
	// marker they wrote, and what is left is the short quoted value nothing
	// reached (#3614). A value that is already a redaction marker is passed
	// over rather than masked again under a different name.
	if p.on("quoted-secret") && strings.ContainsAny(s, "\"'`") && kvAssignmentNearby(lower) {
		s = replaceGroup(s, quotedAssignedSecretRE, 4, "quoted-secret", p, func(m []string) bool {
			return strings.HasPrefix(m[4], "[redacted:")
		})
	}
	if !p.on("credential") {
		return s
	}
	if kvAssignmentNearbyHints(lower, kvIntlHints) {
		intlPatternRuns.Add(1)
		s = replaceSubmatch(s, genericKVIntlRE, "credential", p, func(m []string) string {
			return m[1] + m[2] + m[3] + m[4] + "[redacted:credential]" + closingQuote(m[4], m[6])
		})
	}
	// Its own gate: the adjacency one cannot see a delimiter that is a few
	// words away, which is the whole point of this pattern.
	if strings.ContainsAny(s, ":=") && containsAnyFold(s, kvIntlHints) {
		s = replaceSubmatch(s, genericKVIntlFillerRE, "credential", p, func(m []string) string {
			return m[1] + m[2] + m[3] + m[4] + "[redacted:credential]" + closingQuote(m[4], m[6])
		})
	}
	// Its own gate rather than the key-value one: that gate asks for a
	// delimiter near a key word, and the flag form has neither a colon nor an
	// equals sign when it is written `--password secret`.
	//
	// The dash is part of every hint. "token" and "secret" on their own are
	// ordinary words and the gate would pass on most messages, which is the
	// cost kvAssignmentNearby was written to avoid; the pattern cannot match
	// without a dash before the name, so "-token" is both necessary and rare.
	if flagHintNearby(lower) {
		s = replaceGroup(s, passwordFlagRE, 4, "credential", p, func(m []string) bool {
			return notASecretValue(m[4]) || proseWordAfterFlag(m[3], m[4])
		})
	}
	// A separate gate, and deliberately the older one: `DATABASE_PASSWORD=x`
	// has no dash anywhere, so the flag gate above would have silently switched
	// this pattern off.
	if strings.Contains(lower, "passw") || strings.Contains(lower, "pwd") {
		s = replaceGroup(s, passwordAssignRE, 4, "credential", p, func(m []string) bool {
			return notASecretValue(m[4])
		})
	}
	if strings.Contains(lower, "pass") {
		s = replaceGroup(s, passSuffixAssignRE, 4, "credential", p, func(m []string) bool {
			return notASecretValue(m[4])
		})
	}
	// Its own gate: the prose form has no delimiter for the others to find.
	if strings.Contains(lower, "password") || strings.Contains(lower, "passphrase") ||
		strings.Contains(lower, "парол") {
		s = replaceGroup(s, prosePasswordRE, 4, "credential", p, func(m []string) bool {
			return notASecretValue(m[4]) || !chosenSecret(m[4])
		})
	}
	// Its own gate, on the one thing the pattern needs: a byte outside ASCII.
	// Every other pattern here can only match an ASCII value, so this runs
	// exactly where they cannot.
	if hasNonASCII(s) && (strings.ContainsAny(s, ":=")) {
		s = replaceGroup(s, intlValueRE, 4, "credential", p, func(m []string) bool {
			return !worthRedactingIntl(m[4])
		})
	}
	return s
}

type commandPasswordFlags struct {
	sshpass, userPair, mysql, login bool
}

func isLoginSep(c byte) bool {
	return c == ' ' || c == '=' || c == '<'
}

func hasLoginCommand(w string) bool {
	return strings.Contains(w, "login") || strings.Contains(w, "redis-cli") ||
		strings.Contains(w, "ftp") || strings.Contains(w, "smbclient")
}

// scanCommandPasswordFlags detects candidate command-line password flags in a
// single pass over '-' delimiters in lower.
func scanCommandPasswordFlags(lower string) commandPasswordFlags {
	var flags commandPasswordFlags
	for i := 0; i+1 < len(lower); {
		j := strings.IndexByte(lower[i:], '-')
		if j < 0 {
			break
		}
		k := i + j
		i = k + 1
		if k+1 >= len(lower) {
			break
		}
		next := lower[k+1]
		atStartOrSpace := k == 0 || isSpaceByte(lower[k-1])
		hasSpaceBefore := k > 0 && isSpaceByte(lower[k-1])

		switch next {
		case 'p':
			if !flags.sshpass && strings.Contains(lower[max(0, k-264):k], "sshpass") {
				flags.sshpass = true
			}
			if hasSpaceBefore {
				w := lower[max(0, k-496):k]
				if !flags.mysql && (strings.Contains(w, "mysql") || strings.Contains(w, "mariadb")) {
					flags.mysql = true
				}
				if !flags.login && k+2 < len(lower) && isLoginSep(lower[k+2]) && hasLoginCommand(w) {
					flags.login = true
				}
			}
		case 'a':
			if hasSpaceBefore && !flags.login && k+2 < len(lower) && isLoginSep(lower[k+2]) &&
				hasLoginCommand(lower[max(0, k-496):k]) {
				flags.login = true
			}
		case 'u':
			if atStartOrSpace && !flags.userPair && k+2 < len(lower) && (lower[k+2] == ' ' || lower[k+2] == '=') &&
				strings.IndexByte(lower[k:min(len(lower), k+300)], ':') >= 0 {
				flags.userPair = true
			}
		case '-':
			if !atStartOrSpace {
				continue
			}
			if !flags.userPair && k+6 < len(lower) && lower[k+1:k+6] == "-user" && (lower[k+6] == ' ' || lower[k+6] == '=') &&
				strings.IndexByte(lower[k:min(len(lower), k+300)], ':') >= 0 {
				flags.userPair = true
			}
			if hasSpaceBefore && !flags.login &&
				((k+10 < len(lower) && lower[k+1:k+10] == "-password" && isLoginSep(lower[k+10])) ||
					(k+12 < len(lower) && lower[k+1:k+12] == "-with-token" && isLoginSep(lower[k+12]))) &&
				hasLoginCommand(lower[max(0, k-496):k]) {
				flags.login = true
			}
		}
	}
	return flags
}

// redactCommandPasswords masks passwords passed on a command line, in a netrc
// and in a cookie header.
func redactCommandPasswords(s, lower string, p *kindPass) string {
	if !p.on("command-password") && !p.on("password") && !p.on("cookie") {
		return s
	}
	if p.on("command-password") && strings.IndexByte(lower, '-') >= 0 {
		flags := scanCommandPasswordFlags(lower)
		if flags.sshpass {
			s = replaceGroup(s, sshpassRE, 1, "command-password", p, nil)
		}
		if flags.userPair {
			s = replaceGroup(s, userPairRE, 2, "command-password", p, func(m []string) bool {
				// A uid:gid pair, not a login.
				return allDigits(m[1]) && allDigits(m[2])
			})
		}
		if flags.mysql {
			s = replaceGroup(s, mysqlPassRE, 1, "command-password", p, nil)
		}
		if flags.login {
			s = replaceGroup(s, loginPassRE, 1, "command-password", p, func(m []string) bool {
				// A port, or a host:port — `-p` means that far more often than it
				// means a password.
				return allDigits(strings.TrimLeft(m[1], "0123456789:"))
			})
		}
	}
	if p.on("password") && containsAnchored(lower, "passw", 4) &&
		(strings.Contains(lower, "machine ") || strings.Contains(lower, "login ")) {
		s = replaceGroup(s, netrcRE, 1, "password", p, nil)
	}
	if p.on("cookie") && containsAnchored(lower, "cookie:", 3) {
		s = replaceGroup(s, cookieRE, 1, "cookie", p, nil)
	}
	return s
}

// kindPass is what one redaction pass may write: the counts it fills in, and
// the rules it is allowed to fire at all. A nil allow set is every rule, which
// is what indexing uses.
type kindPass struct {
	counts Counts
	allow  map[string]bool
	fast   bool
	v      *view
}

// finderFor is the finder that answers for a rule, or nil when the plain
// regex is still the one that runs.
func (p *kindPass) finderFor(re *regexp.Regexp, s string) *finder {
	if !p.fast {
		return nil
	}
	f := finders[re]
	if f == nil {
		return nil
	}
	if p.v == nil {
		p.v = &view{}
	}
	p.v.reset(s)
	return f
}

func (p *kindPass) on(kind string) bool { return p.allow == nil || p.allow[kind] }

func (p *kindPass) add(kind string, n int) {
	if n <= 0 {
		return
	}
	if p.counts == nil {
		p.counts = Counts{}
	}
	p.counts.Add(kind, n)
}

func replaceWhole(s string, re *regexp.Regexp, kind string, p *kindPass) string {
	if !p.on(kind) {
		return s
	}
	if f := p.finderFor(re, s); f != nil {
		matches := f.all(p.v)
		p.add(kind, len(matches))
		return spliceMatches(s, matches, func(string) string { return "[redacted:" + kind + "]" })
	}
	n := 0
	out := re.ReplaceAllStringFunc(s, func(_ string) string {
		n++
		return "[redacted:" + kind + "]"
	})
	p.add(kind, n)
	return out
}

func replaceSubmatch(s string, re *regexp.Regexp, kind string, p *kindPass, repl func([]string) string) string {
	if !p.on(kind) {
		return s
	}
	if f := p.finderFor(re, s); f != nil {
		matches := f.all(p.v)
		p.add(kind, len(matches))
		return spliceMatches(s, matches, func(match string) string {
			return repl(re.FindStringSubmatch(match))
		})
	}
	n := 0
	out := re.ReplaceAllStringFunc(s, func(match string) string {
		n++
		return repl(re.FindStringSubmatch(match))
	})
	p.add(kind, n)
	return out
}

// spliceMatches is ReplaceAllStringFunc over matches already found.
func spliceMatches(s string, matches [][]int, repl func(string) string) string {
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	at := 0
	for _, m := range matches {
		b.WriteString(s[at:m[0]])
		b.WriteString(repl(s[m[0]:m[1]]))
		at = m[1]
	}
	b.WriteString(s[at:])
	return b.String()
}

// replaceGroup redacts one capture group of every match and leaves the rest of
// it readable: what makes an argument credential recognisable is the command
// and the flag beside it, and hiding those would cost the recall the line
// exists for. skip, when it returns true, leaves a match alone and counts
// nothing — an over-count here would drift the same way #1569 did.
//
// A value that is a shell reference (`-p "$DEPLOY_PASS"`) is not a secret and
// is left as written.
func replaceGroup(s string, re *regexp.Regexp, group int, kind string, p *kindPass, skip func([]string) bool) string {
	if !p.on(kind) {
		return s
	}
	var matches [][]int
	if f := p.finderFor(re, s); f != nil {
		matches = f.all(p.v)
	} else {
		matches = re.FindAllStringSubmatchIndex(s, -1)
	}
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	at, n := 0, 0
	for _, m := range matches {
		lo, hi := m[2*group], m[2*group+1]
		if lo < 0 || lo < at {
			continue
		}
		groups := make([]string, len(m)/2)
		for i := range groups {
			if m[2*i] >= 0 {
				groups[i] = s[m[2*i]:m[2*i+1]]
			}
		}
		if strings.HasPrefix(groups[group], "$") || strings.HasPrefix(groups[group], Marker) || (skip != nil && skip(groups)) {
			continue
		}
		b.WriteString(s[at:lo])
		b.WriteString("[redacted:" + kind + "]")
		at = hi
		n++
	}
	if n == 0 {
		return s
	}
	b.WriteString(s[at:])
	p.add(kind, n)
	return b.String()
}

// allDigits reports whether every byte is a decimal digit, which is how a
// uid:gid pair is told from a login.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func replaceProvider(s string, p *kindPass) string {
	if f := p.finderFor(providerRE, s); f != nil {
		return spliceMatches(s, f.all(p.v), func(v string) string { return providerMask(v, p) })
	}
	return providerRE.ReplaceAllStringFunc(s, func(v string) string { return providerMask(v, p) })
}

func providerMask(v string, p *kindPass) string {
	kind := "provider-token"
	switch {
	case strings.HasPrefix(v, "ghp_"), strings.HasPrefix(v, "gho_"), strings.HasPrefix(v, "ghs_"),
		strings.HasPrefix(v, "ghu_"), strings.HasPrefix(v, "ghr_"), strings.HasPrefix(v, "github_pat_"):
		kind = "github-token"
	case strings.HasPrefix(v, "sk_live_"), strings.HasPrefix(v, "sk_test_"),
		strings.HasPrefix(v, "rk_live_"), strings.HasPrefix(v, "rk_test_"):
		kind = "stripe-key"
	case strings.HasPrefix(v, "sk-ant-"):
		kind = "anthropic-key"
	case strings.HasPrefix(v, "sk-"):
		kind = "openai-key"
	case strings.HasPrefix(v, "gsk_"):
		kind = "groq-key"
	case strings.HasPrefix(v, "xai-"):
		kind = "xai-key"
	case strings.HasPrefix(v, "hf_"):
		kind = "huggingface-token"
	case strings.HasPrefix(v, "glpat-"):
		kind = "gitlab-token"
	case strings.HasPrefix(v, "npm_"):
		kind = "npm-token"
	case strings.HasPrefix(v, "xoxb-"), strings.HasPrefix(v, "xoxp-"), strings.HasPrefix(v, "xoxc-"), strings.HasPrefix(v, "xoxs-"):
		kind = "slack-token"
	case strings.HasPrefix(v, "AIza"):
		kind = "google-api-key"
	}
	if !p.on(kind) {
		return v
	}
	p.add(kind, 1)
	return "[redacted:" + kind + "]"
}

func closingQuote(open, closer string) string {
	if open == "" {
		return ""
	}
	return closer
}

// ── entropy pass ────────────────────────────────────────────────────────────
// Pattern matching only catches shapes we know. A bare high-entropy string is
// caught here instead — but entropy alone fires on identifiers, hashes and
// paths everywhere (measured: thousands of hits on a real corpus), so a token
// must also sit in a secret-shaped context: the value side of an assignment,
// or alone on its own line.

// Marker is the prefix every replacement shares. Callers count it to report
// how much of a document was already scrubbed at index time, since a later
// pass over redacted text finds nothing left to replace.
const Marker = "[redacted:"

// kinds is every rule name this package writes into a marker.
//
// A reader needs the list because text can hold something that only looks like
// a marker: deja's own documentation of the format, a test fixture, a message
// quoting an earlier answer. Counting those as redactions invented rules
// called `<kind>` and `…` on a real store (#536).
var kinds = map[string]bool{
	"anthropic-key": true, "aws-access-key": true, "aws-key": true, "aws-secret": true,
	"bearer-token": true, "command-password": true, "cookie": true, "credential": true,
	"entropy": true, "github-token": true, "gitlab-token": true, "google-api-key": true,
	"groq-key": true, "huggingface-token": true, "jwt": true, "npm-token": true,
	"openai-key": true, "password": true, "private-key": true, "provider-token": true,
	"quoted-secret": true, "slack-token": true, "stripe-key": true, "telegram-bot-token": true,
	"url-credentials": true, "webhook-url": true, "xai-key": true,
}

// IsKind reports whether a name is a rule this package can write, so a caller
// reading markers back out of text can tell one from a mention of the format.
func IsKind(name string) bool { return kinds[name] }
