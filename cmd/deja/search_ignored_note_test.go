package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2554 gave the listing a line for what the ignore rule withheld. Search filters
// on the same rule inside the index and says nothing: the sessions that asked the
// question are covered by it, so the answer comes back short — or empty — with
// only the tier's own notes on screen (#2562).
func TestSearchSaysWhatTheIgnoreRuleWithheld(t *testing.T) {
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
	write(real, "keeper", "the invoice renderer lost its footer", 2*time.Hour)
	for i, sid := range []string{"scratch1", "scratch2"} {
		write(jobs, sid, "why did the quokka telemetry sharding keep failing", time.Duration(i+1)*time.Hour)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "quokka telemetry sharding") })
	if strings.Contains(out, "scratch") {
		t.Fatalf("an ignored session was served:\n%s", out)
	}
	if !strings.Contains(stderr, "ignore rule") || !strings.Contains(stderr, "2") {
		t.Errorf("two matching sessions were withheld and search said %q", strings.TrimSpace(stderr))
	}

	// Silence when the rule took nothing from this answer.
	quiet := captureStderr(t, func() { _, _ = captureRun(t, "invoice renderer footer") })
	if strings.Contains(quiet, "ignore rule") {
		t.Errorf("the line printed for an answer the rule did not touch: %q", strings.TrimSpace(quiet))
	}
}
