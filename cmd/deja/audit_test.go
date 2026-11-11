package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditReportsClaudeRetentionAndUnwiredNextStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDE_CODE_PROJECT_DIR_NAME", "")
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("DEJA_STORES", "claude")

	projDir := filepath.Join(home, ".claude", "projects", "-Users-alex-app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	prevNow := auditNow
	auditNow = func() time.Time { return now }
	defer func() { auditNow = prevNow }()

	writeTranscript := func(name string, ageDays int) {
		p := filepath.Join(projDir, name)
		line := `{"type":"user","message":{"role":"user","content":"fix jwt rotation"},"timestamp":"2026-10-01T10:00:00Z"}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-time.Duration(ageDays) * 24 * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	writeTranscript("fresh.jsonl", 5)
	writeTranscript("soon.jsonl", 25)
	writeTranscript("expired.jsonl", 34)

	claudeMemDir := filepath.Join(home, ".claude-mem")
	if err := os.MkdirAll(claudeMemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeMemDir, "claude-mem.db"), []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	idxDir := filepath.Join(home, ".cache", "deja")
	var out bytes.Buffer
	if err := runAudit(idxDir, nil, &out); err != nil {
		t.Fatalf("runAudit: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"2 of 3 transcripts at deletion risk (1 past 30d, 1 within 7d; default 30d)",
		"claude-mem.db detected",
		"wiring:   not wired",
		"next:     npx @vshulcz/deja-vu install --auto",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("audit output missing %q\ngot:\n%s", want, text)
		}
	}

	// Verify --json output and custom cleanupPeriodDays=0 (disabled).
	settings := []byte(`{"cleanupPeriodDays": 0}`)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runAudit(idxDir, []string{"--json"}, &out); err != nil {
		t.Fatalf("runAudit --json: %v", err)
	}
	var rep auditReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal audit json: %v\n%s", err, out.String())
	}
	if rep.ClaudeRetention.CleanupPeriodDays != 0 || !rep.ClaudeRetention.Explicit {
		t.Errorf("cleanup_period_days = %+v, want explicit 0", rep.ClaudeRetention)
	}
	if rep.ClaudeRetention.ExpiringIn7Days != 0 || rep.ClaudeRetention.PastPeriod != 0 {
		t.Errorf("expected 0 expiring when cleanupPeriodDays=0, got %+v", rep.ClaudeRetention)
	}
	if !rep.ClaudeMemDBPresent {
		t.Errorf("expected claude_mem_db_present = true")
	}
	if rep.NextCommand != "npx @vshulcz/deja-vu install --auto" {
		t.Errorf("next_command = %q", rep.NextCommand)
	}
}

func TestAuditRejectsUnknownFlagsAndArgs(t *testing.T) {
	var out bytes.Buffer
	if err := runAudit(t.TempDir(), []string{"--bogus"}, &out); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if err := runAudit(t.TempDir(), []string{"extra"}, &out); err == nil {
		t.Fatal("expected error for positional argument")
	}
}
