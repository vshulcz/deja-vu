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
	if !strings.Contains(s, "hook") || !strings.Contains(s, "recall") || !strings.Contains(s, "(empty result)") || !strings.Contains(s, "3 sessions") {
		t.Fatalf("log output = %q", s)
	}

	out.Reset()
	if err := runLogTo(&out, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the injected context body") || !strings.Contains(out.String(), "# hook") {
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
