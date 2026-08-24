package main

import (
	"io"
	"strings"
	"testing"
)

// files has its own flag loop, so the refusals every sibling command makes were
// missing here: a flag with no value, an unknown flag, an empty --project and an
// empty topic all passed through and the answer looked like an answer (#1628).
func TestFilesRefusesWhatDoesNothing(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"retry", "--limit"}, "--limit needs value"},
		{[]string{"retry", "--project"}, "--project needs value"},
		{[]string{"retry", "--project", ""}, "--project needs value"},
		{[]string{"retry", "--unknown"}, "unknown flag"},
		{[]string{""}, "usage"},
	} {
		err := runFiles(dir, tc.args, io.Discard)
		if err == nil {
			t.Errorf("files %v was accepted", tc.args)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("files %v: %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
	// The controls: the shapes that work must keep working. An empty store
	// answers rather than failing, so these are about the parser, not the hits.
	for _, args := range [][]string{
		{"retry"},
		{"retry", "--limit", "3"},
		{"retry", "--project", "api"},
		{"retry", "backoff"},
	} {
		if err := runFiles(dir, args, io.Discard); err != nil {
			t.Errorf("files %v: %v", args, err)
		}
	}
}

// A topic can legitimately start with a dash — `--feature-flag`, `-x`. Refusing
// unknown flags would have left those unaskable, so files takes the same `--`
// escape parseSearch does (#1628).
func TestFilesTakesADashedTopicAfterDoubleDash(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--", "--feature-flag"},
		{"--", "-x"},
		{"--", "--limit", "3"},
	} {
		if err := runFiles(dir, args, io.Discard); err != nil {
			t.Errorf("files %v: %v", args, err)
		}
	}
	// The control: without the escape, a dashed token is still a mistake.
	if err := runFiles(dir, []string{"--feature-flag"}, io.Discard); err == nil {
		t.Error("a dashed topic was taken as a topic without the -- escape")
	}
	// And `--` with nothing after it names no topic.
	if err := runFiles(dir, []string{"--"}, io.Discard); err == nil {
		t.Error("a bare -- was accepted as a topic")
	}
}
