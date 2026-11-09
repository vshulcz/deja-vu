package main

import (
	"io"
	"testing"
)

func TestResumeRejectsBlankPrefix(t *testing.T) {
	dir := hermeticEnv(t)
	want := runResume(dir, nil, io.Discard)
	if want == nil {
		t.Fatal("resume without an id should fail")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"empty", []string{""}},
		{"spaces", []string{"   "}},
		{"tabs and newline", []string{"\t\n"}},
		{"empty before another prefix", []string{"", "sess1234"}},
		{"blank after an option", []string{"--exec", "  "}},
		{"blank after a prefix", []string{"sess1234", "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runResume(dir, tc.args, io.Discard)
			if err == nil || err.Error() != want.Error() {
				t.Fatalf("runResume(%q) = %v; want %v", tc.args, err, want)
			}
		})
	}
}
