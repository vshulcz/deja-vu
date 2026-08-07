package nfcfold

import "testing"

func TestComposeMatchesNFDToNFC(t *testing.T) {
	// NFD = base letter + combining mark, spelled with escapes; NFC = the
	// precomposed rune, also an escape. No source-encoding ambiguity.
	cases := []struct {
		name, nfd, nfc string
	}{
		{"e-acute", "café", "café"},         // café
		{"i-diaeresis", "naïve", "naïve"},   // naïve
		{"n-tilde", "piñata", "piñata"},     // piñata
		{"u-diaeresis", "Zürich", "Zürich"}, // Zürich
		{"c-cedilla", "façade", "façade"},   // façade
	}
	for _, c := range cases {
		if c.nfd == c.nfc {
			t.Fatalf("%s: nfd and nfc are identical bytes — test is trivial", c.name)
		}
		if got := Compose(c.nfd); got != c.nfc {
			t.Errorf("%s: Compose(NFD %q) = %q, want NFC %q", c.name, c.nfd, got, c.nfc)
		}
		if got := Compose(c.nfc); got != c.nfc {
			t.Errorf("%s: Compose(NFC) changed it to %q", c.name, got)
		}
	}
	if got := Compose("plain backpressure"); got != "plain backpressure" {
		t.Errorf("plain text changed: %q", got)
	}
	if got := Compose("́abc"); got != "́abc" {
		t.Errorf("leading combining mark mishandled: %q", got)
	}
}
