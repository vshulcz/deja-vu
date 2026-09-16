package redact

import (
	"strings"
	"testing"
)

// A public key is published by construction, and the entropy pass was masking
// every one of them: it takes the word before the separator as the label, so a
// WireGuard dump's `public key: <base64>` reads as `key:`. On the store this
// was measured against, that class was the largest inside the entropy tier —
// which is also why a user-facing secrets report cannot be built on entropy
// (#536).
//
// The label decides, the way it already decides for `password=`, and only when
// it says public.
func TestAPublicKeyLabelIsNotASecret(t *testing.T) {
	const b64 = "kO8nJ2QxQ1oZk3vVYkq0Z9mE4pC7tRfW8sLxBnMpQ1c="

	kept := []string{
		"public key: " + b64,
		"Public Key: " + b64,
		"public_key=" + b64,
		"PUBKEY=" + b64,
		`"publicKey": "` + b64 + `"`,
		"interface: wg0\n  public key: " + b64 + "\n  listening port: 51820",
	}
	for _, in := range kept {
		out, counts := Text(in)
		if counts.Total() != 0 || strings.Contains(out, Marker) {
			t.Errorf("a published key was masked: %q -> %q %v", first60(in), out, counts)
		}
	}

	// The other direction, which is the whole reason the exception is narrow:
	// a label that does not say public still gets masked.
	masked := []string{
		"api key: " + b64,
		"secret key = " + b64,
		"private key: " + b64,
		"signing key: " + b64,
		"key: " + b64,
		"peer: " + b64,
		// These three belong to the KV credential rule rather than to the
		// entropy pass, so the exception cannot reach them — pinned here
		// because that ownership is the reason it cannot.
		"api_key=" + b64,
		"apikey: " + b64,
		"API_KEY=" + b64,
	}
	for _, in := range masked {
		out, counts := Text(in)
		if counts.Total() == 0 || !strings.Contains(out, Marker) {
			t.Errorf("a secret-shaped value survived: %q -> %q", first60(in), out)
		}
	}
}

func first60(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
