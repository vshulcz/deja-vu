package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A harness's own bookkeeping is not a transcript deja failed to read. Counting
// it said "11 not recognised here" for qwen and "1" for openclaw on a machine
// whose every session was indexed — the number a reader takes as a parser that
// cannot cope with their store (#3676).
func TestAHarnessOwnStateIsNotAnUnreadTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The suite points the readers at its own directories, so the two under
	// test are named explicitly rather than inherited from HOME.
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(home, ".qwen"))
	t.Setenv("DEJA_OPENCLAW_ROOT", filepath.Join(home, ".openclaw", "agents"))

	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// qwen: one chat, and the three files it keeps beside them.
	chats := filepath.Join(home, ".qwen", "projects", "-work-api", "chats")
	write(filepath.Join(chats, "a.jsonl"),
		`{"uuid":"u1","parentUuid":null,"sessionId":"a","timestamp":"2026-09-17T10:00:00Z","type":"user","cwd":"/work/api","version":"0.20.0","message":{"role":"user","parts":[{"text":"why is the migration slow"}]}}`+"\n")
	write(filepath.Join(chats, "a.runtime.json"), `{"model":"qwen3"}`)
	write(filepath.Join(home, ".qwen", "projects", "-work-api", "meta.json"), `{"cwd":"/work/api"}`)
	write(filepath.Join(home, ".qwen", "projects", "-work-api", "extract-cursor.json"), `{}`)

	// openclaw: one session, and the agent's model catalogue.
	sessions := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	write(filepath.Join(sessions, "s1.jsonl"),
		`{"type":"session","id":"s1","timestamp":"2026-09-17T10:00:00Z","cwd":"/work/api"}`+"\n")
	write(filepath.Join(sessions, "sessions.json"), `[]`)
	write(filepath.Join(home, ".openclaw", "agents", "main", "agent", "models.json"), `{"models":[]}`)

	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for _, line := range strings.Split(out.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "qwen ") && !strings.HasPrefix(trimmed, "openclaw ") {
			continue
		}
		if strings.Contains(line, "not recognised here") {
			t.Errorf("a store deja reads whole reports unread files: %s", trimmed)
		}
	}
}
