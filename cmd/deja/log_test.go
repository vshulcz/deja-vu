package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func TestLogEmptyState(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runLogTo(&out, index.DefaultDir(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no usage recorded yet") {
		t.Fatalf("empty log output = %q", out.String())
	}
	out.Reset()
	if err := runLogTo(&out, index.DefaultDir(), []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no injected digests") {
		t.Fatalf("empty --last output = %q", out.String())
	}
}

func TestLogListsEventsAndLastDigest(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigest(dir, usage.KindHook, "the injected context body", 3, 1024)
	usage.RecordResult(dir, usage.KindRecall, 42, 0, true)

	var out bytes.Buffer
	if err := runLogTo(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "session digest") || !strings.Contains(s, "recall") || !strings.Contains(s, "(empty result)") || !strings.Contains(s, "3 sessions") {
		t.Fatalf("log output = %q", s)
	}

	out.Reset()
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the injected context body") || !strings.Contains(out.String(), "# session digest") {
		t.Fatalf("--last output = %q", out.String())
	}

	out.Reset()
	if err := runLogTo(&out, dir, []string{"--json", "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"kind"`) {
		t.Fatalf("json output = %q", out.String())
	}
	if err := runLogTo(&out, dir, []string{"--nope"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestStatuslineShowsDistillRatio(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("d", 1000), 2, 250000)
	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "less than replaying") {
		t.Fatalf("statusline missing ratio: %q", out.String())
	}
}

// A second count used to replace the first without a word (#5074). `last`
// refuses the same input, and log now says the same thing.
func TestLogRefusesASecondCount(t *testing.T) {
	withTempStores(t)
	dir := t.TempDir()
	var b bytes.Buffer
	err := runLogTo(&b, dir, []string{"2", "1"})
	if err == nil {
		t.Fatalf("log 2 1: want an error, got output %q", b.String())
	}
	if want := `log takes one count, got 2 and "1"`; err.Error() != want {
		t.Errorf("log 2 1: got %q, want %q", err.Error(), want)
	}
	if b.Len() != 0 {
		t.Errorf("log 2 1 printed before refusing: %q", b.String())
	}
	// Flags around a single count are still fine.
	b.Reset()
	if err := runLogTo(&b, dir, []string{"--json", "2"}); err != nil {
		t.Errorf("log --json 2: %v", err)
	}
	b.Reset()
	if err := runLogTo(&b, dir, []string{"2"}); err != nil {
		t.Errorf("log 2: %v", err)
	}
}
