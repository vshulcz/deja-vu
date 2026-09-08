package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The OpenClaw row printed a count and nothing else, so a transcript deja
// could not read was invisible there while every other file-listing harness
// named it (#3317). The store keeps files beside the transcripts that the
// reader skips on purpose — the per-session trajectory-path.json, the
// sessions.json list, checkpoint snapshots and the archive of a reset whose
// live file is still there — and none of those is a failure either.
func TestDoctorNamesAnUnreadOpenClawTranscriptButNotItsSidecars(t *testing.T) {
	tmp := hermeticEnv(t)
	agents := filepath.Join(tmp, "openclaw", "agents")
	sessions := filepath.Join(agents, "main", "sessions")
	if err := os.MkdirAll(filepath.Join(sessions, "2026-09"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", agents)

	sid := "4a1b2c3d-0000-4000-8000-000000000001"
	line := `{"type":"message","role":"user","content":"why does the retry loop drop the last attempt","timestamp":"2026-09-01T10:00:00.000Z"}` + "\n"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sid+".jsonl", line)
	write(sid+".trajectory-path.json", `{"path":"/w/api"}`)
	write("sessions.json", `[{"id":"`+sid+`"}]`)
	write(sid+".checkpoint.4a1b2c3d-0000-4000-8000-000000000002.jsonl", line)
	write(sid+".jsonl.reset.1756713600", line)
	// A transcript one directory deeper than the reader looks: the file deja
	// holds nothing from, and the only thing the row has to say.
	if err := os.WriteFile(filepath.Join(sessions, "2026-09", "moved.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	var row string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "openclaw") && strings.Contains(l, "file") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("no openclaw file row in the report at all")
	}
	if !strings.Contains(row, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the one transcript deja did not read", row)
	}
	if strings.Contains(row, "2 not recognised") || strings.Contains(row, "3 not recognised") ||
		strings.Contains(row, "4 not recognised") || strings.Contains(row, "5 not recognised") {
		t.Errorf("row = %q, the files the reader skips on purpose are counted as failures", row)
	}
}
