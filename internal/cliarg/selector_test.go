package cliarg

import (
	"errors"
	"testing"
)

func TestCleanSelector(t *testing.T) {
	const id = "a1b2c3d4-1111-4000-8000-d6e7f8a9b0c1"
	for _, tc := range []struct{ in, want string }{
		{id, id},
		{"  " + id + "  ", id},
		{`"` + id + `"`, id},
		{"'" + id + "'", id},
		{"`" + id + "`", id},
		{"“" + id + "”", id},
		{"‘" + id + "’", id},
		{"`\"" + id + "\"`", id},
		{`"  ` + id + `  "`, id},
		// An unmatched quote on one side is part of the typed token.
		{`"` + id, `"` + id},
		{"", ""},
		{`"`, `"`},
		{`""`, ""},
		{`''`, ""},
		{"``", ""},
		{`"'  '"`, ""},
		{"“  ”", ""},
	} {
		if got := CleanSelector(tc.in); got != tc.want {
			t.Errorf("CleanSelector(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitSelector(t *testing.T) {
	for _, tc := range []struct{ in, harness, id string }{
		{"claude:abc", "claude", "abc"},
		{"cursor-cli:abc", "cursor-cli", "abc"},
		{"abc", "", "abc"},
		{":abc", "", ":abc"},
		{"claude:", "", "claude:"},
		{"2026-08-03T10:00:00Z", "2026-08-03T10", "00:00Z"},
		{"a b:c", "", "a b:c"},
		{"a\tb:c", "", "a\tb:c"},
	} {
		h, id := SplitSelector(tc.in)
		if h != tc.harness || id != tc.id {
			t.Errorf("SplitSelector(%q) = (%q, %q), want (%q, %q)", tc.in, h, id, tc.harness, tc.id)
		}
	}
}

func TestParseSelector(t *testing.T) {
	got, err := ParseSelector("  `\"claude:abc\"`  ")
	if err != nil {
		t.Fatalf("ParseSelector valid: %v", err)
	}
	if got.Clean != "claude:abc" || got.Harness != "claude" || got.ID != "abc" {
		t.Fatalf("ParseSelector = %+v, want Clean=claude:abc Harness=claude ID=abc", got)
	}

	gotIDOnly, err := ParseSelector("'019fa282'")
	if err != nil {
		t.Fatalf("ParseSelector id only: %v", err)
	}
	if gotIDOnly.Clean != "019fa282" || gotIDOnly.Harness != "" || gotIDOnly.ID != "019fa282" {
		t.Fatalf("ParseSelector id only = %+v", gotIDOnly)
	}

	for _, raw := range []string{"", "   ", `""`, `"'  '"`, "``"} {
		if _, err := ParseSelector(raw); !errors.Is(err, ErrEmptySelector) {
			t.Errorf("ParseSelector(%q) err = %v, want ErrEmptySelector", raw, err)
		}
	}
}
