package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

func howFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(cmd, ts string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","sessionId":"ffff0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
	}
	lines := []string{
		rec("go test ./internal/search/ -run Retry", "2026-07-10T10:01:00Z"),
		rec("go test ./internal/search/ -run Retry -v", "2026-07-10T10:02:00Z"),
		rec("golangci-lint run ./...", "2026-07-10T10:03:00Z"),
	}
	if err := os.WriteFile(filepath.Join(store, "ffff0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `how go` asks how this machine runs go. Matching was a raw substring test, so
// golangci-lint answered — and answered first (#1630).
func TestHowDoesNotMatchInsideAnotherWord(t *testing.T) {
	dir := howFixture(t)
	var b bytes.Buffer
	if err := runHow(dir, []string{"go"}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "golangci-lint") {
		t.Errorf("`how go` answered with golangci-lint:\n%s", out)
	}
	if !strings.Contains(out, "go test ./internal/search/") {
		t.Errorf("`how go` lost the go commands:\n%s", out)
	}
	// The controls: the full name still finds the linter, and a multi-word
	// query still narrows.
	b.Reset()
	if err := runHow(dir, []string{"golangci-lint"}, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "golangci-lint run") {
		t.Errorf("`how golangci-lint` lost it:\n%s", b.String())
	}
	b.Reset()
	if err := runHow(dir, []string{"go", "test"}, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "go test") || strings.Contains(b.String(), "golangci") {
		t.Errorf("`how go test`:\n%s", b.String())
	}
}

// The same flag loop files had: what you typed has to do something (#1630).
func TestHowRefusesWhatDoesNothing(t *testing.T) {
	dir := howFixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"go", "--limit"}, "--limit needs value"},
		{[]string{"go", "--project"}, "--project needs value"},
		{[]string{"go", "--project", ""}, "--project needs value"},
		{[]string{"go", "--unknown"}, "unknown flag"},
		{[]string{""}, "usage"},
	} {
		if err := runHow(dir, tc.args, io.Discard); err == nil {
			t.Errorf("how %v was accepted", tc.args)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("how %v: %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
	for _, args := range [][]string{{"go"}, {"go", "--limit", "3"}, {"go", "--project", "api"}, {"--", "-run"}} {
		if err := runHow(dir, args, io.Discard); err != nil {
			t.Errorf("how %v: %v", args, err)
		}
	}
}

// A dash or an underscore is a boundary, not part of the word: people ask for
// the half of a hyphenated tool they remember. Review named this as the cost of
// the first attempt, where both counted as word characters (#1630).
func TestHowFindsTheHalfOfAHyphenatedCommand(t *testing.T) {
	dir := howFixture(t)
	for _, term := range []string{"lint", "golangci", "golangci-lint"} {
		var b bytes.Buffer
		if err := runHow(dir, []string{term}, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "golangci-lint run") {
			t.Errorf("`how %s` lost golangci-lint:\n%s", term, b.String())
		}
	}
	// The control that started all this: `go` must still not match golangci.
	var b bytes.Buffer
	if err := runHow(dir, []string{"go"}, &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "golangci") {
		t.Errorf("`how go` matched inside golangci again:\n%s", b.String())
	}
}
