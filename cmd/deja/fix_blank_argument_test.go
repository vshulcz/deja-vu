package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

// A blank word is not part of the error text. runFix sends every argument
// through here, on both sides of `--` (#5911).
func TestAppendNonBlankDropsWhitespace(t *testing.T) {
	for _, tc := range []struct {
		in, want []string
	}{
		{[]string{"", "   ", "\t"}, nil},
		{[]string{"", "boom", "  ", "tail"}, []string{"boom", "tail"}},
		{[]string{"  boom  "}, []string{"  boom  "}},
		{[]string{"--- FAIL: TestX", "   "}, []string{"--- FAIL: TestX"}},
	} {
		got := appendNonBlank(nil, tc.in...)
		if !slices.Equal(got, tc.want) {
			t.Errorf("appendNonBlank(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// Whitespace is not an error to look up. `deja fix ""` and `deja fix -- "   "`
// have to ask for the error text (#5911).
func TestFixRejectsAWhitespaceOnlyQuery(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	// A pipe would be read once the query is empty. Point stdin at a device
	// so this test asks about the arguments, not about whatever is piped.
	prev := os.Stdin
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = null
	t.Cleanup(func() {
		os.Stdin = prev
		_ = null.Close()
	})

	for _, args := range [][]string{
		{""},
		{"   "},
		{"\t"},
		{"--", ""},
		{"--", "   "},
		{"--", " \t "},
		{"", "  ", "--", "\t"},
		{"--json", ""},
		{"--json", "--", "   "},
	} {
		var out bytes.Buffer
		err := runFix(dir, args, &out)
		if err == nil || !strings.Contains(err.Error(), "give the error text") {
			t.Errorf("deja fix %q: %v\n%s", args, err, out.String())
		}
	}
}

// Blank words beside a real error are dropped, and the error is still the query.
func TestFixKeepsRealTextBesideBlankArguments(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"", "connection refused"},
		{"connection refused", "   "},
		{"--", "   ", "--- FAIL: TestX (0.01s)"},
		{"", "--", "connection refused"},
	} {
		var out bytes.Buffer
		if err := runFix(dir, args, &out); err != nil {
			t.Errorf("deja fix %q: %v", args, err)
		}
	}
}
