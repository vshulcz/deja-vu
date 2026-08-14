package redact

import (
	"math/rand"
	"strings"
	"testing"
)

// The provider gate decides whether the provider-token matcher runs at all, so
// a wrong skip leaves a live credential in the index. This proves it is a
// necessary condition rather than a heuristic: whenever the regex matches, the
// gate must have passed.
func TestProviderGateNeverHidesAMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(20260813))
	// Fragments that land on and around every alternative in the pattern,
	// including the prefixes the gate was tightened to, near-misses of them,
	// and values just under and just over the length each one requires.
	parts := []string{
		"ghp_", "gho_", "ghs_", "ghu_", "ghr_", "ghx_", "gh", "github_pat_",
		"glpat-", "sk_live_", "sk_test_", "rk_live_", "sk-", "gsk_", "xai-",
		"hf_", "npm_", "xoxb-", "xoxp-", "AIza",
		"abcdefghijklmnopqrst", "abcdefghijklmnopqrstuvwxyz0123456789",
		"0123456789012345", "short", " ", "\n", "\t", ".", "-", "_",
		"through", "thought", "enough", "right", "highlight",
	}
	for range 20000 {
		var b strings.Builder
		for n := rng.Intn(8); n >= 0; n-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		s := b.String()
		if providerRE.FindStringIndex(s) != nil && !containsAnyFold(s, providerHints) {
			t.Fatalf("gate skipped a real match: %q", s)
		}
	}
}

// And it has to skip what it exists for. "gh" alone is inside ordinary English,
// which put the regex on nearly every message of a chat history.
func TestProviderGateSkipsOrdinaryEnglish(t *testing.T) {
	for _, s := range []string{
		"I thought the through-line was right there",
		"enough light to highlight the neighbourhood",
		"we might have brought the wrong one tonight",
		"the daughter laughed at the tough dough",
	} {
		if containsAnyFold(s, providerHints) {
			t.Errorf("gate let ordinary English through: %q", s)
		}
	}
}

// The tokens themselves still have to be redacted end to end.
func TestProviderTokensStillRedacted(t *testing.T) {
	for _, s := range []string{
		"ghp_" + strings.Repeat("a", 30),
		"github_pat_" + strings.Repeat("b", 30),
		"sk-" + strings.Repeat("c", 30),
		"AIza" + strings.Repeat("d", 32),
	} {
		out, _ := Text("here it is: " + s)
		if strings.Contains(out, s) {
			t.Errorf("token survived redaction: %q", out)
		}
	}
}
