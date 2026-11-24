package main

import "testing"

func TestParseForgetRejectsQuotedEmptySelector(t *testing.T) {
	for _, raw := range []string{"", "   ", `""`, `"'  '"`} {
		if _, err := parseForgetArgs([]string{"--session", raw}); err == nil {
			t.Errorf("parseForgetArgs(--session %q) succeeded, want error", raw)
		}
	}
	fg, err := parseForgetArgs([]string{"--session", "  `claude:abc`  "})
	if err != nil {
		t.Fatalf("parseForgetArgs valid pasted selector: %v", err)
	}
	if fg.o.Session != "claude:abc" {
		t.Fatalf("fg.o.Session = %q, want %q", fg.o.Session, "claude:abc")
	}
}

func TestParseSearchSessionCleansAndRejectsQuotedEmptySelector(t *testing.T) {
	o, err := parseSearch([]string{"--session='claude:abc'", "query"})
	if err != nil {
		t.Fatalf("parseSearch valid pasted selector: %v", err)
	}
	if o.Session != "claude:abc" {
		t.Fatalf("o.Session = %q, want %q", o.Session, "claude:abc")
	}
	for _, raw := range []string{`""`, `"'  '"`} {
		if _, err := parseSearch([]string{"--session", raw, "query"}); err == nil {
			t.Errorf("parseSearch(--session %q) succeeded, want error", raw)
		}
	}
}

func TestParseShowRejectsQuotedEmptySelector(t *testing.T) {
	for _, raw := range []string{`""`, `"'  '"`} {
		if _, err := parseShow([]string{raw}); err == nil {
			t.Errorf("parseShow(%q) succeeded, want error", raw)
		}
	}
	o, err := parseShow([]string{"  '019fa282'  "})
	if err != nil {
		t.Fatalf("parseShow valid pasted selector: %v", err)
	}
	if o.id != "019fa282" {
		t.Fatalf("o.id = %q, want %q", o.id, "019fa282")
	}
}
