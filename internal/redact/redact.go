package redact

import (
	"math"
	"sync/atomic"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/query"
	"os"
	"regexp"
	"strings"
)

type Counts map[string]int

func (c Counts) Add(kind string, n int) {
	if n > 0 {
		c[kind] += n
	}
}

func (c Counts) Total() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

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
	genericKVRE = regexp.MustCompile(`(?i)\b([\w.-]{0,64}?(?:api[_-]?key|secret|token|passwd|password|authorization))(\\*['"]?\s*[:=]\s*)(\\*['"]?)([A-Za-z0-9/+=._-]{16,})(\\*['"]?)`)
	// An environment variable holding a credential does not have to say "api" or
	// "token" in its name: DEJA_EMBED_KEY, GROQ_KEY, VOYAGE_KEY all end in plain
	// _KEY, which genericKVRE never matched, so an opaque value — one with no
	// provider prefix and not enough entropy for the last-resort pass — was
	// indexed in the clear. Measured on `export DEJA_EMBED_KEY=9f2b…f83`.
	//
	// Case-sensitive on purpose: this is the shell shape, and matching `_key`
	// as well would take `cache_key: <16 chars>` out of every YAML file people
	// paste, which costs recall for no secret.
	envKeyRE = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*_KEY)(\\*['"]?\s*[:=]\s*)(\\*['"]?)([A-Za-z0-9/+=._-]{16,})(\\*['"]?)`)
	// The same shape in the languages people actually type in. \b is ASCII-only
	// in RE2, so a Cyrillic or CJK key word can never sit behind it — these get
	// their own pattern. A Russian speaker writing "пароль: …" had the secret
	// stored in the clear because every pattern here was English-only.
	genericKVIntlRE = regexp.MustCompile(`(?i)(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호)(\\*['"]?\s*[:=]\s*)(\\*['"]?)([A-Za-z0-9/+=._-]{16,})(\\*['"]?)`)
	// The same key words with a few of their own between the word and the
	// colon. "пароль от стейджа: …" is how the line is actually written, and
	// genericKVIntlRE needs the delimiter to follow the word directly. The
	// value class is unchanged — 16 or more ASCII characters — so a sentence
	// that merely mentions a token ("токен лежит в файле: строка 12") still
	// matches nothing.
	genericKVIntlFillerRE = regexp.MustCompile(`(?i)(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호)([^\n:=]{1,32}[:=]\s*)(\\*['"]?)([A-Za-z0-9/+=._-]{16,})(\\*['"]?)`)
	bearerRE              = regexp.MustCompile(`(?i)\b(Bearer|Basic)(\s+)([A-Za-z0-9._~+/=-]{16,})`)
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
	quotedSecretRE = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(\s+(?:is\s+|was\s+|for\s+)?)(\\*["'` + "`" + `])([^"'` + "`" + `\n]{6,80})(\\*["'` + "`" + `])`)
	pemPrivateRE   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----.*?-----END [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----`)
	// A key pasted into a transcript is often not pasted whole: the output was
	// truncated, the session ended, the tail landed in another message. The
	// closing marker was required, so the half that carries the key material
	// was the half that went through (#2409). This runs after the whole-block
	// pattern and takes the header plus the base64 lines under it — the lines,
	// not the rest of the message, so the prose around a key survives.
	// At least one body line: a bare header carries nothing, and eating it
	// alone would hide the marker that lets the whole-block pattern pair it
	// with a body that arrives in the next field.
	pemPrivateOpenRE = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----(?:[ \t]*\r?\n[A-Za-z0-9+/=]{16,}[ \t]*)+`)
	// Provider prefixes. sk- allows internal hyphens/underscores so modern
	// hyphenated formats (sk-ant-…, sk-proj-…) are covered, not just legacy
	// sk-<alnum> keys. xai- stays alphanumeric-only: real xAI keys have no
	// internal hyphens, and allowing them makes every long kebab-case slug
	// that happens to start with "xai-" (branch names, doc titles) a false
	// positive.
	providerRE = regexp.MustCompile(`\b(gh[opsur]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}|sk-[A-Za-z0-9_-]*[A-Za-z0-9]{20,}|gsk_[A-Za-z0-9]{20,}|xai-[A-Za-z0-9]{20,}|hf_[A-Za-z0-9]{20,}|npm_[A-Za-z0-9]{30,}|xox[bpcs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})\b`)
	jwtRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\b`)
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
	cookieRE = regexp.MustCompile(`(?i)\b(?:set-)?cookie:[ \t]*([^\n]{8,400})`)
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
	// Nobody writes `--password` in prose: the flag is machine input, and what
	// follows it is the value whatever its length.
	passwordFlagRE = regexp.MustCompile(`(?i)(^|\s)(--?(?:password|passwd|pwd))([ =]\\*['"]?)([^\s'"]{3,128})`)
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
	intlValueRE = regexp.MustCompile(`(?i)(парол[ьяею]|токен[ауы]?|секрет[ауы]?|ключ[аеиуом]?|contraseña|senha|passwort|密码|密碼|パスワード|비밀번호|api[_-]?key|secret|token|passwd|password)([^\n:=]{0,32}[:=]\s*)(\\*['"]?)([^\s'"]*[^\x00-\x7f][^\s'"]*)(\\*['"]?)`)
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
	for i := 0; i < len(s); i++ {
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

func Text(s string) (string, Counts) {
	counts := Counts{}
	if Disabled() || s == "" {
		return s, counts
	}
	lower := strings.ToLower(s)
	if strings.Contains(s, "-----BEGIN") {
		s = replaceWhole(s, pemPrivateRE, "private-key", counts)
		s = replaceWhole(s, pemPrivateOpenRE, "private-key", counts)
	}
	if strings.Contains(s, "://") {
		s = replaceSubmatch(s, connURLRE, "url-credentials", counts, func(m []string) string {
			return m[1] + m[2] + ":[redacted:url-credentials]@" + m[4]
		})
	}
	if strings.Contains(lower, "aws") {
		s = replaceSubmatch(s, awsSecretRE, "aws-secret", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:aws-secret]" + closingQuote(m[3], m[5])
		})
	}
	if strings.Contains(s, "AKIA") || strings.Contains(s, "ASIA") {
		s = replaceWhole(s, awsAccessKeyRE, "aws-access-key", counts)
	}
	if strings.Contains(lower, "password") || strings.Contains(lower, "passwd") ||
		strings.Contains(lower, "pwd") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "api key") ||
		strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
		s = replaceSubmatch(s, quotedSecretRE, "quoted-secret", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:quoted-secret]" + m[5]
		})
	}
	if strings.Contains(lower, "bearer") || strings.Contains(lower, "basic ") {
		s = replaceSubmatch(s, bearerRE, "bearer-token", counts, func(m []string) string {
			return m[1] + m[2] + "[redacted:bearer-token]"
		})
	}
	if strings.Contains(s, "eyJ") {
		s = replaceWhole(s, jwtRE, "jwt", counts)
	}
	if kvAssignmentNearby(lower) {
		s = replaceSubmatch(s, genericKVRE, "credential", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
		s = replaceSubmatch(s, envKeyRE, "credential", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
	}
	if kvAssignmentNearbyHints(lower, kvIntlHints) {
		intlPatternRuns.Add(1)
		s = replaceSubmatch(s, genericKVIntlRE, "credential", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
	}
	// Its own gate: the adjacency one cannot see a delimiter that is a few
	// words away, which is the whole point of this pattern.
	if strings.ContainsAny(s, ":=") && containsAnyFold(s, kvIntlHints) {
		s = replaceSubmatch(s, genericKVIntlFillerRE, "credential", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
	}
	// Its own gate rather than the key-value one: that gate asks for a
	// delimiter near a key word, and the flag form has neither a colon nor an
	// equals sign when it is written `--password secret`.
	if strings.Contains(lower, "passw") || strings.Contains(lower, "pwd") {
		s = replaceGroup(s, passwordFlagRE, 4, "credential", counts, func(m []string) bool {
			return notASecretValue(m[4])
		})
	}
	// Its own gate, on the one thing the pattern needs: a byte outside ASCII.
	// Every other pattern here can only match an ASCII value, so this runs
	// exactly where they cannot.
	if hasNonASCII(s) && (strings.ContainsAny(s, ":=")) {
		s = replaceGroup(s, intlValueRE, 4, "credential", counts, func(m []string) bool {
			return !worthRedactingIntl(m[4])
		})
	}
	if strings.Contains(lower, "sshpass") {
		s = replaceGroup(s, sshpassRE, 1, "command-password", counts, nil)
	}
	if strings.Contains(lower, "-u ") || strings.Contains(lower, "-u=") || strings.Contains(lower, "--user") {
		s = replaceGroup(s, userPairRE, 2, "command-password", counts, func(m []string) bool {
			// A uid:gid pair, not a login.
			return allDigits(m[1]) && allDigits(m[2])
		})
	}
	if strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb") {
		s = replaceGroup(s, mysqlPassRE, 1, "command-password", counts, nil)
	}
	if strings.Contains(lower, "login") || strings.Contains(lower, "redis-cli") ||
		strings.Contains(lower, "ftp") || strings.Contains(lower, "smbclient") {
		s = replaceGroup(s, loginPassRE, 1, "command-password", counts, func(m []string) bool {
			// A port, or a host:port — `-p` means that far more often than it
			// means a password.
			return allDigits(strings.TrimLeft(m[1], "0123456789:"))
		})
	}
	if (strings.Contains(lower, "password") || strings.Contains(lower, "passwd")) &&
		(strings.Contains(lower, "machine ") || strings.Contains(lower, "login ")) {
		s = replaceGroup(s, netrcRE, 1, "password", counts, nil)
	}
	if strings.Contains(lower, "cookie:") {
		s = replaceGroup(s, cookieRE, 1, "cookie", counts, nil)
	}
	if containsAnyFold(s, providerHints) {
		s = replaceProvider(s, counts)
	}
	s = redactEntropy(s, counts)
	return s, counts
}

func replaceWhole(s string, re *regexp.Regexp, kind string, counts Counts) string {
	n := 0
	out := re.ReplaceAllStringFunc(s, func(_ string) string {
		n++
		return "[redacted:" + kind + "]"
	})
	counts.Add(kind, n)
	return out
}

func replaceSubmatch(s string, re *regexp.Regexp, kind string, counts Counts, repl func([]string) string) string {
	n := 0
	out := re.ReplaceAllStringFunc(s, func(match string) string {
		n++
		return repl(re.FindStringSubmatch(match))
	})
	counts.Add(kind, n)
	return out
}

// replaceGroup redacts one capture group of every match and leaves the rest of
// it readable: what makes an argument credential recognisable is the command
// and the flag beside it, and hiding those would cost the recall the line
// exists for. skip, when it returns true, leaves a match alone and counts
// nothing — an over-count here would drift the same way #1569 did.
//
// A value that is a shell reference (`-p "$DEPLOY_PASS"`) is not a secret and
// is left as written.
func replaceGroup(s string, re *regexp.Regexp, group int, kind string, counts Counts, skip func([]string) bool) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
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
		if strings.HasPrefix(groups[group], "$") || (skip != nil && skip(groups)) {
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
	counts.Add(kind, n)
	return b.String()
}

// allDigits reports whether every byte is a decimal digit, which is how a
// uid:gid pair is told from a login.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func replaceProvider(s string, counts Counts) string {
	return providerRE.ReplaceAllStringFunc(s, func(v string) string {
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
		counts.Add(kind, 1)
		return "[redacted:" + kind + "]"
	})
}

func closingQuote(open, close string) string {
	if open == "" {
		return ""
	}
	return close
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

const (
	entropyMinBits       = 4.5
	entropyMinAssign     = 20
	entropyMinStandalone = 28
)

func shannonBits(s string) float64 {
	counts := map[byte]int{}
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	var h float64
	n := float64(len(s))
	for _, c := range counts {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

func charClasses(s string) int {
	var lower, upper, digit, other bool
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			other = true
		}
	}
	n := 0
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			n++
		}
	}
	return n
}

func isHexish(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		hexish := c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-'
		if !hexish {
			return false
		}
	}
	return true
}

// looksLikePath reports whether a token is a filesystem path rather than
// something that merely contains slashes. Rooted, more than one separator, and
// carrying none of the punctuation a credential or a URL brings with it — a
// base64 blob has no leading slash, and `https://…` and `key:value` both fail
// on the characters they are named for.
func looksLikePath(tok string) bool {
	if !strings.HasPrefix(tok, "/") && !strings.HasPrefix(tok, "~/") &&
		!strings.HasPrefix(tok, "./") && !strings.HasPrefix(tok, "../") {
		return false
	}
	if strings.Count(tok, "/") < 2 {
		return false
	}
	// '=' is base64 padding and the assignment a secret arrives in; ':' is a
	// scheme or a key. Either one means this is not a bare path.
	return !strings.ContainsAny(tok, "=:")
}

func entropyCandidate(tok string) bool {
	if len(tok) > 256 || isHexish(tok) || charClasses(tok) < 3 {
		return false
	}
	// Lowercase-only path segments sneak into the charset via '/' and '-';
	// real secrets with slashes (base64) mix cases.
	if strings.Contains(tok, "/") && strings.ToLower(tok) == tok {
		return false
	}
	// A filesystem path is not a blob whatever its case. The lowercase rule
	// above was standing in for "looks like base64", and a macOS scratch
	// directory defeats it twice over: `-Users-<name>` supplies the uppercase
	// and a uuid directory supplies the entropy, so `/private/tmp/…/<uuid>/…`
	// scored as a secret. When the record was nothing but that path — the
	// output of a `pwd` — the whole message became `[redacted:entropy]`, which
	// destroys the content rather than masking it, and says a key was removed
	// when none was there.
	if looksLikePath(tok) {
		return false
	}
	return shannonBits(tok) >= entropyMinBits
}

// assignmentValue reports whether s[start] begins the value side of an
// assignment: a word, then = or :, optional quote/space, then the token.
// Prose and log lines assign nothing — a key that is an English stop word
// ("moved to: <blob>", "at: <hash>") does not count.
func assignmentValue(s string, start int) bool {
	i := start - 1
	for i >= 0 && (s[i] == '"' || s[i] == '\'' || s[i] == ' ' || s[i] == '\t') {
		i--
	}
	if i < 0 || (s[i] != '=' && s[i] != ':') {
		return false
	}
	i--
	for i >= 0 && (s[i] == '"' || s[i] == '\'' || s[i] == ' ' || s[i] == '\t') {
		i--
	}
	end := i + 1
	for i >= 0 && isWordByte(s[i]) {
		i--
	}
	key := s[i+1 : end]
	digitsOnly := true
	for k := 0; k < len(key); k++ {
		if key[k] < '0' || key[k] > '9' {
			digitsOnly = false
			break
		}
	}
	// A pure-digit key is the Telegram bot-token shape (12345678:AA…) — keep
	// it. Otherwise require a real word: two-letter keys are log noise.
	if digitsOnly {
		return len(key) >= 6
	}
	if len(key) < 3 {
		return false
	}
	return !query.IsStopWord(strings.ToLower(key))
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

// standaloneLine reports whether the token is the only content on its line —
// the shape of a pasted credential.
func standaloneLine(s string, start, end int) bool {
	i := start - 1
	for i >= 0 && s[i] != '\n' {
		if s[i] != ' ' && s[i] != '\t' && s[i] != '\r' {
			return false
		}
		i--
	}
	j := end
	for j < len(s) && s[j] != '\n' {
		if s[j] != ' ' && s[j] != '\t' && s[j] != '\r' {
			return false
		}
		j++
	}
	return true
}

// entropySpans finds the runs entropyTokenRE describes — twenty or more
// characters from [A-Za-z0-9+/_-], plus up to two trailing '=' — without the
// regexp engine.
//
// This pass is the only one with no cheap gate in front of it: every message
// pays it, matching or not. Profiling a real rebuild put redaction at 23.5% of
// index CPU and this scan at a third of that, spent almost entirely on text
// that turns out to hold nothing. A byte loop does the same job; the filtering
// that follows is unchanged, so what gets redacted does not move.
func entropySpans(s string) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); {
		if !isEntropyByte(s[i]) {
			i++
			continue
		}
		run := i
		for i < len(s) && isEntropyByte(s[i]) {
			i++
		}
		if i-run < entropyMinAssign {
			continue
		}
		end := i
		for pad := 0; pad < 2 && end < len(s) && s[end] == '='; pad++ {
			end++
		}
		out = append(out, [2]int{run, end})
		i = end
	}
	return out
}

func isEntropyByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '+', c == '/', c == '_', c == '-':
		return true
	}
	return false
}

func redactEntropy(s string, counts Counts) string {
	if len(s) < entropyMinAssign {
		return s
	}
	spans := entropySpans(s)
	if spans == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, span := range spans {
		tok := s[span[0]:span[1]]
		if strings.Contains(tok, "[redacted:") {
			continue
		}
		hit := false
		if len(tok) >= entropyMinAssign && assignmentValue(s, span[0]) && entropyCandidate(tok) {
			hit = true
		} else if len(tok) >= entropyMinStandalone && standaloneLine(s, span[0], span[1]) && entropyCandidate(tok) {
			hit = true
		}
		if !hit {
			continue
		}
		b.WriteString(s[last:span[0]])
		b.WriteString("[redacted:entropy]")
		last = span[1]
		counts.Add("entropy", 1)
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}
