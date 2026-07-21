package redact

import (
	"math"

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
	awsSecretRE    = regexp.MustCompile(`(?i)\b(aws[_-]?secret[_-]?access[_-]?key)(\s*['"]?\s*[:=]\s*)(['"]?)([A-Za-z0-9/+=_-]{32,})(['"]?)`)
	// The key may be embedded in a larger identifier (ANTHROPIC_API_KEY,
	// x-api-key) and, in JSON, a closing quote can sit between the key and the
	// delimiter ("api_key": "..."). Tolerate both so env-var and JSON forms are
	// caught, not just a bare `api_key=`.
	genericKVRE  = regexp.MustCompile(`(?i)\b([\w.-]{0,64}?(?:api[_-]?key|secret|token|passwd|password|authorization))(\s*['"]?\s*[:=]\s*)(['"]?)([A-Za-z0-9/+=._-]{16,})(['"]?)`)
	bearerRE     = regexp.MustCompile(`(?i)\b(Bearer|Basic)(\s+)([A-Za-z0-9._~+/=-]{16,})`)
	pemPrivateRE = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----.*?-----END [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-----`)
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
)

func Disabled() bool { return os.Getenv("DEJA_NO_REDACT") == "1" }

// kvHints are the substrings genericKVRE can anchor on; providerHints the
// literal prefixes of providerRE. Checking them first keeps the regexes off
// the vast majority of messages, which contain no credentials at all.
var kvHints = []string{"key", "secret", "token", "passw", "authorization"}

// "github_pat_" is listed on its own: "gh" is not a substring of "github".
var providerHints = []string{"gh", "github_pat_", "glpat-", "sk_", "rk_", "sk-", "gsk_", "xai-", "hf_", "npm_", "xox", "AIza"}

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
	if strings.Contains(lower, "bearer") || strings.Contains(lower, "basic ") {
		s = replaceSubmatch(s, bearerRE, "bearer-token", counts, func(m []string) string {
			return m[1] + m[2] + "[redacted:bearer-token]"
		})
	}
	if strings.Contains(s, "eyJ") {
		s = replaceWhole(s, jwtRE, "jwt", counts)
	}
	if containsAnyFold(lower, kvHints) {
		s = replaceSubmatch(s, genericKVRE, "credential", counts, func(m []string) string {
			return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
		})
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

var entropyTokenRE = regexp.MustCompile(`[A-Za-z0-9+/_-]{20,}={0,2}`)

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
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
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

func redactEntropy(s string, counts Counts) string {
	if len(s) < entropyMinAssign {
		return s
	}
	spans := entropyTokenRE.FindAllStringIndex(s, -1)
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
