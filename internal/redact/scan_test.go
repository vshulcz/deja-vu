package redact

import "testing"

func TestScanKindsIgnoresDisabledAndDetectsCredentials(t *testing.T) {
	t.Setenv("DEJA_NO_REDACT", "1")
	if !Disabled() {
		t.Fatal("expected DEJA_NO_REDACT=1 to disable Text redaction")
	}

	antKey := "sk-ant-" + "api03-1234567890abcdefghij1234567890abcdefghij"
	ghTok := "ghp_" + "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890"
	awsKey := "AKIA" + "IOSFODNN7EXAMPLE77"
	bearerTok := "Bearer " + "tok_live_9876543210abcdef9876543210"
	pem := "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAnExampleLineOne\n-----END " + "RSA PRIVATE KEY-----"
	sample := "export key=" + antKey + "\n\x1b[31m" + ghTok + "\x1b[0m\n" + awsKey + "\nAuthorization: " + bearerTok + "\n" + pem

	allow := map[string]bool{
		"anthropic-key":  true,
		"github-token":   true,
		"aws-access-key": true,
		"bearer-token":   true,
		"private-key":    true,
	}
	counts := ScanKinds(sample, allow)
	for _, want := range []string{"anthropic-key", "github-token", "aws-access-key", "bearer-token", "private-key"} {
		if counts[want] < 1 {
			t.Errorf("ScanKinds missing %s (got %v)", want, counts)
		}
	}
	if got, c := Text(sample); got != sample || c.Total() != 0 {
		t.Fatalf("Text must remain disabled when DEJA_NO_REDACT=1; got %d counts", c.Total())
	}
}
