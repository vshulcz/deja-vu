package redact

import (
	"strings"
	"testing"
)

// #1919 documents `export DEJA_EMBED_KEY=…` as the way to reach an authenticated
// embedding endpoint, which puts that line in the shell an agent is watching.
// A key with a provider prefix was already caught and a high-entropy one too,
// but a plain opaque token named by a variable ending in _KEY was not: every
// pattern wanted the words api_key, secret, token or password in the name.
func TestEnvVarKeyIsRedacted(t *testing.T) {
	for _, tc := range []struct{ name, line, secret string }{
		{"opaque hex", `export DEJA_EMBED_KEY=9f2b7c4e1a8d6350bb91ee27ac4d0f83`, "9f2b7c4e1a8d6350bb91ee27ac4d0f83"},
		{"quoted", `export GROQ_KEY='gsk0000aaaa1111bbbb2222cccc'`, "gsk0000aaaa1111bbbb2222cccc"},
		{"json", `{"VOYAGE_KEY": "pa00000000111111112222222233"}`, "pa00000000111111112222222233"},
		{"colon form", `DEEPINFRA_KEY: 0123456789abcdef0123456789`, "0123456789abcdef0123456789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, counts := Text(tc.line)
			if strings.Contains(out, tc.secret) {
				t.Fatalf("secret survived ingest:\n  in:  %s\n  out: %s", tc.line, out)
			}
			if counts.Total() == 0 {
				t.Fatal("nothing was counted, so `deja sources` would report a clean store")
			}
		})
	}
}

// The reason this pattern is case-sensitive. Configuration files are full of
// lowercase keys whose values are ordinary strings, and redacting those costs
// recall while protecting nothing.
func TestLowercaseKeyNamesAreLeftAlone(t *testing.T) {
	for _, line := range []string{
		`cache_key: user-profile-v2-20260825`,
		`  partition_key = "customer_id_and_region"`,
		`sort_key: created_at_descending_index`,
	} {
		out, _ := Text(line)
		if strings.Contains(out, "[redacted") {
			t.Errorf("redacted an ordinary configuration value:\n  in:  %s\n  out: %s", line, out)
		}
	}
}
