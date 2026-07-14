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
	awsAccessKeyRE = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	awsSecretRE    = regexp.MustCompile(`(?i)\b(aws[_-]?secret[_-]?access[_-]?key)(\s*[:=]\s*)(['"]?)([A-Za-z0-9/+=_-]{32,})(['"]?)`)
	genericKVRE    = regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|passwd|password|authorization)(\s*[:=]\s*)(['"]?)([A-Za-z0-9/+=._-]{16,})(['"]?)`)
	bearerRE       = regexp.MustCompile(`(?i)\b(Bearer)(\s+)([A-Za-z0-9._~+/=-]{16,})`)
	pemPrivateRE   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	providerRE     = regexp.MustCompile(`\b(ghp_[A-Za-z0-9_]{20,}|gho_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,}|npm_[A-Za-z0-9]{30,}|xox[bpcs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})\b`)
	jwtRE          = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	connURLRE      = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://)([^\s/@:]+):([^\s/@]+)@([^\s]+)`) // scheme://user:pass@host
)

func Disabled() bool { return os.Getenv("DEJA_NO_REDACT") == "1" }

func Text(s string) (string, Counts) {
	counts := Counts{}
	if Disabled() || s == "" {
		return s, counts
	}
	s = replaceWhole(s, pemPrivateRE, "private-key", counts)
	s = replaceSubmatch(s, connURLRE, "url-credentials", counts, func(m []string) string {
		return m[1] + m[2] + ":[redacted:url-credentials]@" + m[4]
	})
	s = replaceSubmatch(s, awsSecretRE, "aws-secret", counts, func(m []string) string {
		return m[1] + m[2] + m[3] + "[redacted:aws-secret]" + closingQuote(m[3], m[5])
	})
	s = replaceWhole(s, awsAccessKeyRE, "aws-access-key", counts)
	s = replaceSubmatch(s, bearerRE, "bearer-token", counts, func(m []string) string {
		return m[1] + m[2] + "[redacted:bearer-token]"
	})
	s = replaceWhole(s, jwtRE, "jwt", counts)
	s = replaceSubmatch(s, genericKVRE, "credential", counts, func(m []string) string {
		return m[1] + m[2] + m[3] + "[redacted:credential]" + closingQuote(m[3], m[5])
	})
	s = replaceProvider(s, counts)
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
		case strings.HasPrefix(v, "ghp_"), strings.HasPrefix(v, "gho_"), strings.HasPrefix(v, "github_pat_"):
			kind = "github-token"
		case strings.HasPrefix(v, "sk-"):
			kind = "openai-key"
		case strings.HasPrefix(v, "npm_"):
			kind = "npm-token"
		case strings.HasPrefix(v, "xoxb-"), strings.HasPrefix(v, "xoxp-"):
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
