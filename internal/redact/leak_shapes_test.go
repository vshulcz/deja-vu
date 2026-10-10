package redact

import (
	"strings"
	"testing"
)

// Shapes a secret takes in real transcripts. Every value below is synthetic.

// Real sk-proj- and sk-ant- keys carry '-' and '_' in the body. The match
// stopped at the last '-' before an '_' and the tail went through.
func TestProviderKeyTailIsMasked(t *testing.T) {
	cases := map[string]string{
		"openai":    "OPENAI_API_KEY=sk-proj-Ab3FAKEdE5fG7hI9jK1lM3nO5pQ7rS9tU1vW3xY5zA7bC9dE1fG3hI5jK7lM9n-O1pQ3rS5tU7vW9xY1zA3_bC5dE7fG9hI1jK3lM5nO_FAKETAIL",
		"anthropic": "export ANTHROPIC_API_KEY=sk-ant-api03-Xq3FAKEbN7vK2mPq9LrT4wYz8Hc1Dj6Fg0Ks5Ml_Np2Qr7St3Uv9Wx4Yz1Ab6Cd-Ef4GhFAKETAIL9xQAA",
	}
	for name, in := range cases {
		out, _ := Text(in)
		if strings.Contains(out, "FAKETAIL") {
			t.Errorf("%s: part of the key survived redaction: %q", name, out)
		}
	}
}

// A truncated private key whose lines are indented, as in a YAML block
// scalar (helm values, k8s manifests).
func TestIndentedTruncatedKeyIsMasked(t *testing.T) {
	in := "tls:\n  key: |\n    -----BEGIN RSA PRIVATE KEY-----\n    MIIEpAIBAAKCAQEAindentedFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n    YYindentedSECONDlineFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n(output truncated)"
	out, _ := Text(in)
	if strings.Contains(out, "MIIEpAIBAAKCAQEAindented") || strings.Contains(out, "YYindentedSECONDline") {
		t.Errorf("indented private key body survived: %q", out)
	}
}

// A service-account JSON printed raw, cut before -----END. The line breaks
// inside the key are the two characters `\n`.
func TestJSONEscapedTruncatedKeyIsMasked(t *testing.T) {
	in := `{"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7FAKEFAKEFAKEFAKE\nZZFAKEbodyLINEtwoFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE`
	out, _ := Text(in)
	if strings.Contains(out, "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7") || strings.Contains(out, "ZZFAKEbodyLINEtwo") {
		t.Errorf("escaped private key body survived: %q", out)
	}
}

// An ASCII-armoured PGP secret key cut short, with header lines and a blank
// line before the base64.
func TestTruncatedPGPKeyIsMasked(t *testing.T) {
	in := "-----BEGIN PGP PRIVATE KEY BLOCK-----\nComment: fake\n\nlQOYBGFAKEpgpBODYlineONEfakefakefakefakefakefakefakefakefake\nPGPsecondLINEfakefakefakefakefakefakefakefakefakefakefakefake\n(truncated)"
	out, _ := Text(in)
	if strings.Contains(out, "lQOYBGFAKEpgpBODYlineONE") {
		t.Errorf("PGP private key body survived: %q", out)
	}
}

// `Authorization: Token <40 hex>` (Django REST framework, GitHub's legacy
// `token` scheme).
func TestAuthorizationTokenSchemeIsMasked(t *testing.T) {
	for _, in := range []string{
		`curl -H "Authorization: Token 9944b09199c62bcf9418ad846dd0e4bbdfc6ee4b" https://api.example.com/`,
		`Authorization: token 9944b09199c62bcf9418ad846dd0e4bbdfc6ee4b`,
	} {
		out, _ := Text(in)
		if strings.Contains(out, "9944b09199c62bcf9418ad846dd0e4bbdfc6ee4b") {
			t.Errorf("authorization credential survived: %q", out)
		}
	}
}

// A Slack incoming-webhook URL is a credential whole: anyone holding it can
// post to the channel.
func TestWebhookURLIsMasked(t *testing.T) {
	// Joined at run time so the fake URL is not a literal a push scanner flags.
	in := "curl -X POST https://hooks." + "slack.com/services/T0FAKE000/B0FAKE000/FAKEfakeFAKEfake12345678 -d '{\"text\":\"hi\"}'"
	out, _ := Text(in)
	if strings.Contains(out, "FAKEfakeFAKEfake12345678") {
		t.Errorf("webhook secret survived: %q", out)
	}
}

// Outbound turns home paths into ~/…, and recap and the stats card rely on it,
// in every shape a transcript holds one.
func TestOutboundMasksEveryHomePathForm(t *testing.T) {
	for _, in := range []string{
		"read ~/.claude/projects/-Users-alicehunt-src-app/abc.jsonl", // Claude's encoded project dir
		`{"cwd":"C:\\Users\\alicehunt\\src"}`,                        // JSON-escaped Windows path
		"/Users/alicehunt Smithhunt/src/app/main.go",                 // name with a space: surname kept
		"/Users/алисаhunt/src/app",                                   // non-ASCII account name
	} {
		out, _ := Outbound(in)
		if strings.Contains(out, "alicehunt") || strings.Contains(out, "Smithhunt") || strings.Contains(out, "алисаhunt") {
			t.Errorf("account name survived Outbound: %q -> %q", in, out)
		}
	}
}
