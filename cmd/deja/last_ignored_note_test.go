package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every rule in deja that withholds rows says how many: the trust policy prints
// its own line, search says "showing 36 of 39", the brief counts what it filled
// in for. The ignore rule says nothing — and since #2541 it takes rows out of
// the listing too, so on this machine 253 of 400 disappeared with no line
// anywhere connecting them to the rule (#2554).
func TestLastSaysWhatTheIgnoreRuleWithheld(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	real := filepath.Join(claude, "projects", "-tmp-app")
	jobs := filepath.Join(claude, ".claude", "jobs", "abc", "projects", "-tmp-app")
	for _, d := range []string{real, jobs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	write := func(dir, sid, text string, ago time.Duration) {
		rec := claudeRecord(t, map[string]any{
			"type": "user", "sessionId": sid, "cwd": "/tmp/app",
			"timestamp": time.Now().Add(-ago).UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text},
		})
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(real, "keeper", "the pool starves under load", 2*time.Hour)
	for i, sid := range []string{"scratch1", "scratch2", "scratch3"} {
		write(jobs, sid, "one-shot question "+sid, time.Duration(i+1)*time.Hour)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "last") })
	// The premise: the listing kept the real session and dropped the rest.
	if !strings.Contains(out, "keeper") {
		t.Fatalf("the real session is not listed, so this measures nothing:\n%s", out)
	}
	if strings.Contains(out, "scratch") {
		t.Fatalf("an ignored session was listed:\n%s", out)
	}
	if !strings.Contains(stderr, "3") || !strings.Contains(stderr, "ignore") {
		t.Errorf("the listing dropped three sessions and said %q", strings.TrimSpace(stderr))
	}
	// And it names the rule and where it lives, the way the trust policy's own
	// line does — a person who wrote the pattern has to be able to find it.
	if !strings.Contains(stderr, ".claude/jobs") || !strings.Contains(stderr, "policy.json") {
		t.Errorf("the line does not say which rule or where: %q", strings.TrimSpace(stderr))
	}

	// Silence when the rule takes nothing: this is a state, not a decoration.
	if err := os.RemoveAll(jobs); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	quiet := captureStderr(t, func() { _, _ = captureRun(t, "last") })
	if strings.Contains(quiet, "ignore") {
		t.Errorf("the line printed with nothing withheld: %q", strings.TrimSpace(quiet))
	}
}
