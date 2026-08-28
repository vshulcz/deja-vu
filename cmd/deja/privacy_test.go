package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
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
	// A restore that finds no tombstone refuses, so a script can tell it from
	// one that worked (#2263).
	if _, err := captureRun(t, "forget", "--unforget", "missing"); err == nil {
		t.Fatal("unforget of a missing tombstone succeeded")
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
	// On every store line, not merely somewhere in the output: three separate
	// places print this count, so asserting the string alone passed with the
	// harness loop's copy removed — another line carried it.
	var checked int
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(line, "\t")
		if !ok || name == "" || !strings.Contains(rest, "sessions=") {
			continue
		}
		checked++
		if !strings.Contains(line, "excluded-patterns=1") {
			t.Errorf("the %s line does not name the active patterns: %q", name, line)
		}
	}
	// Enough lines to be the listing rather than one store that happens to
	// carry it; the exact number is deja's business, not this test's.
	if checked < 5 {
		t.Fatalf("wrong fixture: only %d store lines in\n%s", checked, out)
	}
}

// The transcript on disk does not change when a session is forgotten, so the
// incremental pass — every ordinary `deja index` — skips it and the session
// stays invisible after unforget. Only a full rebuild brought it back (#672).
func TestUnforgetBringsTheSessionBackWithoutAFullRebuild(t *testing.T) {
	seedTouchedIndex(t, 2, "/w/t/shared.go")
	dir := index.DefaultDir()
	before, err := index.Search(dir, search.Options{Query: "pool sizing", All: true})
	if err != nil || len(before) != 2 {
		t.Fatalf("seed: %d sessions err=%v", len(before), err)
	}
	if _, err := captureRun(t, "forget", "--session", "t00"); err != nil {
		t.Fatal(err)
	}
	gone, err := index.Search(dir, search.Options{Query: "pool sizing", All: true})
	if err != nil || len(gone) != 1 {
		t.Fatalf("after forget: %d sessions err=%v", len(gone), err)
	}
	out, err := captureRun(t, "forget", "--unforget", "t00")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "restored 1 session") {
		t.Errorf("unforget said %q — nothing tells the reader it worked", out)
	}
	// No `deja index` in between: the command has to leave the index usable.
	back, err := index.Search(dir, search.Options{Query: "pool sizing", All: true})
	if err != nil || len(back) != 2 {
		t.Fatalf("after unforget: %d sessions err=%v — the session did not come back", len(back), err)
	}
	_, err = captureRun(t, "forget", "--unforget", "nothing-like-this")
	if err == nil {
		t.Error("a prefix matching nothing was reported as a restore")
	} else if !strings.Contains(err.Error(), "no tombstone matches") {
		t.Errorf("a prefix matching nothing said %q", err)
	}
}

// --before takes a duration or a date, so an error naming neither leaves the
// reader guessing which one they got wrong (#678).
func TestForgetBeforeErrorNamesBothForms(t *testing.T) {
	withTempStores(t)
	_, err := captureRun(t, "forget", "--before", "yesterdayish")
	if err == nil {
		t.Fatal("a bad --before succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"yesterdayish", "30d", "2026-01-31"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}
