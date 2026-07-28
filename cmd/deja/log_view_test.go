package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja log` is the audit trail: what was injected, when, and under which
// policy. It is how someone checks that memory is actually being served, and
// what was in it.
func TestLogRendersTheAuditTrail(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()

	var empty bytes.Buffer
	if err := runLogTo(&empty, dir, nil); err != nil {
		t.Fatal(err)
	}
	// An empty log has to explain itself rather than print nothing — the
	// same rule the other commands follow.
	if strings.TrimSpace(empty.String()) == "" {
		t.Fatal("empty log printed nothing at all")
	}

	usage.RecordDigestPolicy(dir, usage.KindHook, "<deja-recall>\n  - Session: **a** `1`\n", 1, 4096, "local+imported")
	usage.RecordDigestPolicy(dir, usage.KindRecall, "<deja-recall>\n  - Session: **b** `2`\n", 2, 8192, "local-only")

	var out bytes.Buffer
	if err := runLogTo(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"hook", "recall"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %q:\n%s", want, got)
		}
	}

	// --last shows the digest itself, which is the point: seeing what the
	// agent was actually given.
	var last bytes.Buffer
	if err := runLogTo(&last, dir, []string{"--last"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last.String(), "Session") {
		t.Fatalf("--last did not print the digest:\n%s", last.String())
	}

	// --json is consumed by scripts, so it has to parse.
	var jsonOut bytes.Buffer
	if err := runLogTo(&jsonOut, dir, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var parsed any
	if err := json.Unmarshal(jsonOut.Bytes(), &parsed); err != nil {
		t.Fatalf("--json is not json: %v\n%s", err, jsonOut.String())
	}

	// A count limits the output; garbage is refused rather than ignored.
	var limited bytes.Buffer
	if err := runLogTo(&limited, dir, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"--nope", "0", "-3", "abc"} {
		if err := runLogTo(&bytes.Buffer{}, dir, []string{bad}); err == nil {
			t.Fatalf("log accepted %q", bad)
		}
	}
}

// `deja view` writes the whole memory as one local HTML file. It is the
// privacy-sensitive surface people share screenshots of, so what it will and
// will not do with its arguments is worth pinning.
func TestViewWritesLocalHTML(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	out := filepath.Join(t.TempDir(), "memory.html")
	if err := runView(dir, []string{"--out", out, "--no-open"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("view wrote no file: %v", err)
	}
	body := string(b)
	if !strings.Contains(strings.ToLower(body), "<html") {
		t.Fatalf("output is not a page:\n%.200s", body)
	}
	// Everything has to be inline: a page that fetches from the network is
	// no longer "nothing leaves your machine".
	for _, forbidden := range []string{"http://", "https://cdn", "<script src="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("page reaches outside for %q", forbidden)
		}
	}
	if err := runView(dir, []string{"--out"}); err == nil {
		t.Fatal("--out without a path was accepted")
	}
	if err := runView(dir, []string{"--nope"}); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}
