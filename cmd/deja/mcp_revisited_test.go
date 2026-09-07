package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The MCP answer is what an agent acts on without opening the session, so the
// note that the session reversed itself has to be there too — this is the
// surface #2976 was reported from: recall said "we are not on ClawHub yet" out
// of a session that later records the submission going through.
func TestRecallSaysTheSessionCameBackToIt(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text, at string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "flip", "cwd": "/proj", "timestamp": at,
			"message": map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := line("user", "clawhub clawhub clawhub — checked, we are not listed", "2026-08-20T10:00:00Z") +
		line("user", "clawhub clawhub, still nothing under our name", "2026-08-21T10:00:00Z") +
		line("assistant", "clawhub clawhub, no listing", "2026-08-22T10:00:00Z") +
		line("user", strings.Repeat("filler ", 200), "2026-08-24T10:00:00Z") +
		line("assistant", "the clawhub listing is live, moderation passed", "2026-08-27T10:00:00Z")
	if err := os.WriteFile(filepath.Join(store, "flip.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	answer, err := recallText(dir, "clawhub", "", 5, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "comes back to this later") {
		t.Fatalf("the recall answer does not say the session revisits this:\n%s", answer)
	}
}
