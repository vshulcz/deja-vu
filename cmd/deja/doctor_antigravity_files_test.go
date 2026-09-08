package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Antigravity keeps everything under `.system_generated`, and the dot-rule
// that trims codex's `.tmp` noise silenced the row completely: a transcript
// deja could not read was invisible, while the store's own bookkeeping — the
// full transcripts, the chunk files, the message records — was invisible too
// (#3377).
func TestDoctorNamesAnUnreadAntigravityTranscriptButNotItsBookkeeping(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "antigravity")
	brain := filepath.Join(root, "brain", "f017fad5", ".system_generated")
	for _, d := range []string{
		filepath.Join(brain, "logs", "chunks", "transcript"),
		filepath.Join(brain, "messages"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`
	write(filepath.Join(brain, "logs", "transcript.jsonl"), line+"\n")
	write(filepath.Join(brain, "logs", "transcript_full.jsonl"), line+"\n")
	write(filepath.Join(brain, "logs", "chunks", "transcript", "0.jsonl"), line+"\n")
	write(filepath.Join(brain, "messages", "8f1c2a3e-0000-4000-8000-000000000000.json"), "{}")
	write(filepath.Join(root, "brain", "f017fad5", "task.md.metadata.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "antigravity") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no antigravity file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the rest of the store is its own bookkeeping", got)
	}
	// And a transcript under a name the reader does not take is what the row
	// is for.
	write(filepath.Join(brain, "logs", "transcript.v2.jsonl"), line+"\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the transcript deja did not read", got)
	}
}

// The two shapes the first cut swallowed: a transcript restored beside the
// conversations rather than inside one, and a session whose only artefact is
// the full log — the row said nothing about either (review of #3377).
func TestDoctorNamesAnAntigravityTranscriptInTheWrongPlace(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "antigravity")
	logs := filepath.Join(root, "brain", "sessA", ".system_generated", "logs")
	onlyFull := filepath.Join(root, "brain", "sessB", ".system_generated", "logs")
	restored := filepath.Join(root, "restored_backup")
	for _, d := range []string{logs, onlyFull, restored, filepath.Join(root, "cache")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)

	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nwhy does the retry loop drop the last attempt\n</USER_REQUEST>"}`
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(logs, "transcript.jsonl"))
	write(filepath.Join(logs, "transcript_full.jsonl")) // the second copy: not a loss
	write(filepath.Join(onlyFull, "transcript_full.jsonl"))
	write(filepath.Join(restored, "transcript.jsonl"))
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "antigravity") && strings.Contains(l, "file") {
			if !strings.Contains(l, "2 not recognised here") {
				t.Errorf("row = %q, want both the restored transcript and the session whose only log is the full one", l)
			}
			return
		}
	}
	t.Fatal("no antigravity file row in the report at all")
}
