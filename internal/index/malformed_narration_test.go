package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line deja cannot parse is dropped and counted, and until #1993 the index
// run reported only what survived: ten turns on disk, "6 messages" on screen,
// and the four that vanished visible only through `deja doctor`. The narration
// already names a store it could not open; a line it could not parse is the
// same loss.
func TestAnIndexRunSaysWhatItCouldNotRead(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))

	turn := func(role, text string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "s1", "cwd": "/tmp/app",
			"timestamp": "2026-08-20T10:00:00Z",
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Ten turns; four carry a raw escape inside the JSON string, which is what
	// a harness writes when it captures terminal output into a field without
	// escaping it. A raw control byte inside a JSON string is invalid JSON, so
	// claude_decode.go drops the whole line.
	var body strings.Builder
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		line := turn(role, "turn about pgbouncer number "+string(rune('0'+i)))
		if i >= 3 && i <= 6 {
			line = strings.Replace(line, "turn about", "turn \x1b[31mabout", 1)
		}
		body.WriteString(line + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second store, all of it readable. The count belongs to the store that
	// lost the lines: a fold that hands every store the run-wide total reads
	// the same on a one-store fixture, which is what this store is here to
	// stop.
	rollout := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	if err := os.MkdirAll(rollout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rollout, "rollout-2026-01-01T12-00-00-c1.jsonl"),
		[]byte(`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"c1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"a clean codex turn"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	var out strings.Builder
	if err := Ensure(dir, "", true, &out); err != nil {
		t.Fatal(err)
	}
	said := out.String()

	// The premise: four turns really are gone. Without this the case would pass
	// on a fixture whose lines all parsed, saying nothing about the silence.
	got, ok, err := FindByIdentity(dir, "claude", "s1")
	if err != nil || !ok {
		t.Fatalf("the session did not come back from the index: %v %v", ok, err)
	}
	if len(got.Messages) != 6 {
		t.Fatalf("fixture indexed %d of 10 turns, so it does not measure a drop", len(got.Messages))
	}
	if !strings.Contains(said, "6 message") {
		t.Fatalf("the run did not narrate the claude store at all: %q", said)
	}

	// The count of what was lost, in the line that reports what was kept. A
	// mention that omits the number ("some lines were skipped") leaves a person
	// unable to tell one bad write from half the store.
	if !strings.Contains(said, "4 line") {
		t.Errorf("an index run that dropped 4 unreadable lines never said so:\n%s", said)
	}
	if !strings.Contains(said, "deja: codex:") {
		t.Fatalf("the clean store never narrated, so nothing here checks attribution:\n%s", said)
	}
	for _, line := range strings.Split(said, "\n") {
		if strings.HasPrefix(line, "deja: codex:") && strings.Contains(line, "could not be read") {
			t.Errorf("the claude store's loss was charged to codex too: %q", line)
		}
	}
}
