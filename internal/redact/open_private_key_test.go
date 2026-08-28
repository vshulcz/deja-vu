package redact

import (
	"strings"
	"testing"
)

// A private key pasted into a transcript is usually not pasted whole: the
// output was truncated, the session ended, the tail landed in another message.
// The pattern required the closing marker, so the case where the body survives
// was the case deja let through (#2409).
func TestAPrivateKeyWithNoEndMarker(t *testing.T) {
	body := strings.Repeat("b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gt\n", 3)
	cases := []struct {
		name string
		text string
	}{
		{"whole", "here is the key:\n-----BEGIN OPENSSH PRIVATE KEY-----\n" + body +
			"-----END OPENSSH PRIVATE KEY-----\nthat is all"},
		{"cut off", "the deploy key got cut off:\n-----BEGIN OPENSSH PRIVATE KEY-----\n" + body +
			"(output truncated)"},
		{"cut off at the end of the message", "-----BEGIN RSA PRIVATE KEY-----\n" + body},
	}
	for _, tc := range cases {
		got, counts := Text(tc.text)
		if strings.Contains(got, "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ") {
			t.Errorf("%s: key material survived redaction:\n%s", tc.name, got)
		}
		if counts["private-key"] == 0 {
			t.Errorf("%s: nothing was counted as a private key: %v", tc.name, counts)
		}
	}

	// The words around a key are not a key: redaction that eats the prose
	// around it takes the session's meaning with it.
	kept, _ := Text("we generate one with ssh-keygen -t ed25519 and store it in the vault")
	if !strings.Contains(kept, "ssh-keygen -t ed25519") {
		t.Errorf("ordinary text about keys was redacted: %q", kept)
	}
}
