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
