package model

import (
	"encoding/json"
	"testing"
)

// LoggedID claims to be what encoding/json does to a string, so it is pinned
// against the encoder itself rather than against hand-written expectations: a
// lone 0xff, a truncated multi-byte sequence, a surrogate half, and a
// replacement character that was already there (#2199).
func TestLoggedIDIsWhatTheEncoderWrites(t *testing.T) {
	for _, in := range []string{
		"",
		"deja-2026-08-27-app",
		"a<b&c",
		string([]byte{0xff}),
		string([]byte{0xff, 0xfe}),
		"head" + string([]byte{0xe2, 0x82}) + "tail",
		"� was already here",
		string([]byte{0xed, 0xa0, 0x80}),
		string([]byte{0xf0, 0x9f}),
	} {
		raw, err := json.Marshal([]string{in})
		if err != nil {
			t.Fatal(err)
		}
		var back []string
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if got := LoggedID(in); got != back[0] {
			t.Errorf("LoggedID(%q) = %q, the log holds %q", in, got, back[0])
		}
	}
}
