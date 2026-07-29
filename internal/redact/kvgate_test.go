package redact

import (
	"math/rand"
	"strings"
	"testing"
)

// The key-value gate is an optimisation that decides whether a secret matcher
// runs at all. If it is ever wrong in the direction of skipping, a credential
// stays in the index — so this proves the gate is a necessary condition rather
// than a heuristic: whenever the regex matches, the gate must have passed.
func TestKVGateNeverHidesAMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(20260729))
	// Fragments chosen to land near the boundary: hint words, assignment
	// punctuation, quoting, and values just under and just over the length the
	// pattern requires.
	parts := []string{
		"api_key", "api-key", "apikey", "secret", "token", "passwd", "password",
		"Authorization", "AUTHORIZATION", "my.api_key", "x-secret-token",
		"=", ":", " = ", " : ", "\"", "'", " ", "\t", "\n",
		"abcdefghijklmnop", "abcdefghijklmnopqrstuvwxyz012345", "short",
		"the", "handler", "returns", "500", "sk-", "Bearer",
	}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(8); n >= 0; n-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		s := b.String()
		matched := genericKVRE.FindStringIndex(s) != nil
		if matched && !kvAssignmentNearby(strings.ToLower(s)) {
			t.Fatalf("gate skipped a real match: %q", s)
		}
	}
}

// And the gate has to actually skip the case it exists for, or it is only
// overhead: ordinary chatter containing every hint word and no assignment.
func TestKVGateSkipsOrdinaryChatter(t *testing.T) {
	for _, s := range []string{
		"the auth token expires after 15m",
		"read the refresh secret from the environment",
		"we pass an api_key to the client",
		"check the Authorization header",
		"the key insight was that the pool was too small",
	} {
		if kvAssignmentNearby(strings.ToLower(s)) {
			t.Fatalf("gate passed on text with no assignment: %q", s)
		}
		if genericKVRE.FindStringIndex(s) != nil {
			t.Fatalf("the regex itself matched, so the gate is not the issue: %q", s)
		}
	}
}

// The shapes that must still be caught, end to end through Text.
func TestKVGatePassesRealCredentials(t *testing.T) {
	for _, s := range []string{
		`api_key = "sk-abcdefghijklmnopqrstuvwxyz012345"`,
		`API_KEY: abcdefghijklmnopqrstuvwx`,
		"my.api_key='abcdefghijklmnopqrstuvwx'",
		`{"secret":"abcdefghijklmnopqrstuvwx"}`,
		"password\t=\tabcdefghijklmnopqrstuvwx",
		"x-secret-token: abcdefghijklmnopqrstuvwx",
	} {
		out, counts := Text(s)
		if counts.Total() == 0 {
			t.Fatalf("nothing redacted in %q", s)
		}
		if strings.Contains(out, "abcdefghijklmnopqrstuvwx") && !strings.Contains(out, "[redacted") {
			t.Fatalf("secret survived: %q -> %q", s, out)
		}
	}
}
