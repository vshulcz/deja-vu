package sources

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The parsers used to cut a message to 64 KiB with a raw byte slice before the
// index ever redacted it. That split multibyte runes and, worse, capped below
// the index's own redaction boundary, so a secret past 64 KiB was either dropped
// or, when it straddled the cut, half-stored before redaction could see it.
func TestCapParsedMessageKeepsRunesAndReach(t *testing.T) {
	// A rune that lands astride the cap must not be split into invalid UTF-8.
	pad := strings.Repeat("a", maxParsedMessage-1)
	got := capParsedMessage(pad + "€aaaa") // '€' starts at maxParsedMessage-1
	if !utf8.ValidString(got) {
		t.Fatalf("cap produced invalid UTF-8 at the boundary")
	}

	// Text past 64 KiB but within the cap survives, so ingest can redact it
	// whole instead of the parser truncating it away first.
	secret := "ghp_" + strings.Repeat("Z", 36)
	msg := strings.Repeat("x", 70*1024) + secret
	kept := capParsedMessage(msg)
	if !strings.Contains(kept, secret) {
		t.Fatalf("cap dropped content past 64 KiB that redaction still needs to see")
	}

	// Below the cap the text is returned untouched.
	if capParsedMessage("short") != "short" {
		t.Fatalf("cap altered a message under the limit")
	}
}
