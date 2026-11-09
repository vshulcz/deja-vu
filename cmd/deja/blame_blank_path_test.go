package main

import (
	"strings"
	"testing"
)

// The missing-path check only looked at path == "" after the loop, so a blank
// path got through (`deja blame "   "`, also after `--`), and an empty first
// argument was quietly replaced by the next one (`deja blame "" main.go`) (#5938).
func TestBlameRefusesABlankPath(t *testing.T) {
	for _, args := range [][]string{
		{"   "},
		{"\t"},
		{"--", "   "},
		{"--json", "--", " "},
		{""},
		{"--", ""},
		{"", "main.go"},
		{"  ", "main.go"},
		{"--", "", "main.go"},
	} {
		path, _, _, err := parseBlame(args)
		if err == nil {
			t.Errorf("parseBlame(%q) accepted path %q", args, path)
			continue
		}
		if !strings.Contains(err.Error(), "blame needs a path") {
			t.Errorf("parseBlame(%q): got %v, want the missing-path error", args, err)
		}
	}
}

// A real path is unchanged, including one that only starts or ends with a
// space and a dash-led name after `--`.
func TestBlameKeepsARealPath(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"main.go"}, "main.go"},
		{[]string{"--", "-report.md"}, "-report.md"},
		{[]string{" main.go"}, " main.go"},
		{[]string{"--json", "main.go:12"}, "main.go:12"},
	} {
		path, _, _, err := parseBlame(tc.args)
		if err != nil || path != tc.want {
			t.Errorf("parseBlame(%q) = %q, %v; want %q", tc.args, path, err, tc.want)
		}
	}
}
