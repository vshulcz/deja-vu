package redact

import (
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
