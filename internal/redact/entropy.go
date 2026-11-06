package redact

import (
	"math"
	"strings"

	"github.com/vshulcz/deja-vu/internal/query"
)

const (
	entropyMinBits       = 4.5
	entropyMinAssign     = 20
	entropyMinStandalone = 28
)

func shannonBits(s string) float64 {
	counts := map[byte]int{}
	for i := range len(s) {
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
	for i := range len(s) {
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
	for i := range len(s) {
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
	if looksLikePath(tok) || looksLikeGoTest(tok) {
		return false
	}
	return shannonBits(tok) >= entropyMinBits
}

// looksLikeGoTest reports whether tok is a Go test/subtest identifier
// (`TestFoo/sub_case`, `BenchmarkBar_16`). Go test output writes `--- FAIL:
// TestName/subtest`, and a long CamelCase + snake_case subtest path crosses
// 4.5 bits of Shannon entropy without carrying any secret.
func looksLikeGoTest(tok string) bool {
	if strings.ContainsAny(tok, "+=") || !strings.ContainsAny(tok, "/_") {
		return false
	}
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if rest, ok := strings.CutPrefix(tok, p); ok && len(rest) > 0 && (rest[0] >= 'A' && rest[0] <= 'Z' || rest[0] >= '0' && rest[0] <= '9' || rest[0] == '_') {
			return true
		}
	}
	return false
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
	for k := range len(key) {
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
	if labelSaysPublic(s, i+1, key) {
		return false
	}
	return !query.IsStopWord(strings.ToLower(key))
}

// labelSaysPublic reports whether the label in front of the value says the
// value is published, in which case masking it removes something a reader is
// entitled to and a search is expected to match.
//
// The entropy pass takes the word before the separator as the label, so a
// WireGuard dump — `public key: <base64>`, one per interface and one per peer —
// reads as `key:` and every line becomes `[redacted:entropy]`. On a real corpus
// that was the largest single class inside the entropy tier, ahead of npm
// lockfile integrity hashes, and none of it is a credential: a public key is
// public by construction.
//
// The rest of the redactor already follows the label — `password=` is treated
// as a password because it says so — and this is the same rule in the other
// direction. It does not cover `peer:`, which names a party rather than
// claiming to be public, and it does not touch the pattern rules: a PEM private
// key block is caught by its own rule whatever the line around it says.
func labelSaysPublic(s string, keyStart int, key string) bool {
	switch strings.ToLower(key) {
	case "pubkey", "publickey", "public_key", "public-key", "fail", "skip":
		return true
	case "pass":
		i := keyStart - 1
		for i >= 0 && (s[i] == ' ' || s[i] == '\t') {
			i--
		}
		return i >= 2 && s[i-2:i+1] == "---"
	case "key", "keys":
		// The word before it, if any: "public key: …" but not "api key: …".
		i := keyStart - 1
		for i >= 0 && (s[i] == ' ' || s[i] == '\t' || s[i] == '"' || s[i] == '\'') {
			i--
		}
		end := i + 1
		for i >= 0 && isWordByte(s[i]) {
			i--
		}
		return strings.EqualFold(s[i+1:end], "public")
	}
	return false
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

func redactEntropy(s string, p kindPass) string {
	if !p.on("entropy") || len(s) < entropyMinAssign {
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
		p.add("entropy", 1)
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}
