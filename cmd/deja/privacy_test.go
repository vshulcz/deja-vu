package main

import (
	"strings"
	"testing"
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
