package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
)

func TestPrivacyCommandFlags(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "forget"); err == nil {
		t.Fatal("forget without selector succeeded")
	}
	if _, err := captureRun(t, "forget", "--unknown"); err == nil {
		t.Fatal("unknown forget flag succeeded")
	}
	if _, err := captureRun(t, "forget", "--before", "not-a-date"); err == nil {
		t.Fatal("bad date succeeded")
	}
	if _, err := captureRun(t, "stats", "--redaction", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "stats", "--redaction"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "missing", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--before", "2020-01-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--unforget", "missing"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "forget", "--list")
	if err != nil || strings.Contains(out, "claude:") {
		t.Fatalf("list=%q err=%v", out, err)
	}
}

func TestPrivacyCommandBranches(t *testing.T) {
	withTempStores(t)

	for _, args := range [][]string{
		{"forget", "--session"},
		{"forget", "--project"},
		{"forget", "--before"},
		{"forget", "--unforget"},
	} {
		if _, err := captureRun(t, args...); err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
	if _, err := captureRun(t, "stats", "--redaction", "--card"); err == nil {
		t.Fatal("redaction card combination unexpectedly succeeded")
	}
	if _, err := captureRun(t, "stats"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "forget", "--before", "2099-01-01")
	if err != nil || !strings.Contains(out, "sessions dropped:") {
		t.Fatalf("forget output=%q err=%v", out, err)
	}
	out, err = captureRun(t, "forget", "--list")
	if err != nil || !strings.Contains(out, "claude:") {
		t.Fatalf("tombstone list=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "forget", "--unforget", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--before", "1h", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"2026-01-02T03:04:05Z", "2026-01-02", "2026-01-02 03:04:05"} {
		if got, err := parseForgetDate(value); err != nil || got.IsZero() {
			t.Fatalf("parseForgetDate(%q) = %v, %v", value, got, err)
		}
	}
	if _, err := parseForgetDate("not a date"); err == nil {
		t.Fatal("invalid date parsed")
	}
}

func TestRedactionReportRendersRulesAndSidecar(t *testing.T) {
	withTempStores(t)
	root := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	secret := "api_key=" + strings.Repeat("a", 16)
	path := root + "/session.jsonl"
	if err := os.WriteFile(path, []byte(`{"type":"user","sessionId":"report-session","timestamp":"2026-01-02T10:00:00Z","message":{"role":"user","content":"`+secret+`"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(embed.Path(index.DefaultDir()), []byte("sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "stats", "--redaction")
	if err != nil || !strings.Contains(out, "Redactions") || !strings.Contains(out, "Sidecar") || !strings.Contains(out, "claude") {
		t.Fatalf("report=%q err=%v", out, err)
	}
}

func TestSourcesReportsActiveExclusions(t *testing.T) {
	withTempStores(t)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "project")
	out, err := captureRun(t, "sources")
	if err != nil || !strings.Contains(out, "excluded-patterns=1") || !strings.Contains(out, "excluded-sessions=") {
		t.Fatalf("sources=%q err=%v", out, err)
	}
}
