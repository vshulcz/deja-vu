package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gemini row walked all of ~/.gemini while the reader only looks at
// tmp/<project>/chats, so Antigravity's store — a sibling directory inside the
// same root, with its own row — was counted as chats gemini failed to read:
// 55 of the 77 on this machine, the rest gemini's own configuration (#3397).
func TestDoctorDoesNotCountAnotherHarnessAsGeminisUnread(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "gemini")
	chats := filepath.Join(root, "tmp", "proj", "chats")
	brain := filepath.Join(root, "antigravity", "brain", "f017fad5", ".system_generated", "logs")
	for _, d := range []string{chats, brain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_GEMINI_ROOT", root)
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(chats, "session-2026-08-22T06-32-6b36be8d.jsonl"),
		`{"sessionId":"6b36be8d","type":"user","content":"why does the retry loop drop the last attempt"}`+"\n")
	write(filepath.Join(brain, "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-08T14:18:27Z","content":"<USER_REQUEST>\nanything\n</USER_REQUEST>"}`+"\n")
	write(filepath.Join(root, "settings.json"), "{}")
	write(filepath.Join(root, "projects.json"), "{}")

	row := func() string {
		t.Helper()
		var buf bytes.Buffer
		doctorHarnesses(&buf, t.TempDir())
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "gemini") && strings.Contains(l, "file") {
				return l
			}
		}
		t.Fatal("no gemini file row in the report at all")
		return ""
	}
	if got := row(); strings.Contains(got, "not recognised") {
		t.Errorf("row = %q, want no such note — the other store and the settings are not gemini's chats", got)
	}
	// And a chat the reader does not take is still what the row is for: the
	// shape a renamed directory has, which is what layout drift looks like.
	moved := filepath.Join(root, "tmp", "proj", "conversations")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(moved, "session-2026-08-22T06-40-6b36be8d.jsonl"), "{}\n")
	if got := row(); !strings.Contains(got, "1 not recognised here") {
		t.Errorf("row = %q, want it to name the chat deja did not read", got)
	}
}
