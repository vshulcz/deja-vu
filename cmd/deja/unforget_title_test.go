package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// forget clears the title a promoted note borrowed from its source so the
// forgotten session's first line does not survive in the note (#666). unforget
// is the moment that reason stops applying, and the title stayed cleared for
// good (#969).
func TestUnforgetGivesAPromotedNoteItsBorrowedTitleBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the decision: pool cap stays at 20"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"s11","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s11.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s11"); err != nil {
		t.Fatal(err)
	}

	title := func(t *testing.T) string {
		t.Helper()
		b, err := os.ReadFile(os.Getenv("DEJA_NOTES_FILE"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if kind, _ := m["kind"].(string); kind == "promoted" {
				s, _ := m["title"].(string)
				return s
			}
		}
		t.Fatal("no promoted note in the file")
		return ""
	}

	if got := title(t); !strings.Contains(got, "pool cap") {
		t.Fatalf("promote did not borrow a title: %q", got)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s11"); err != nil {
		t.Fatal(err)
	}
	if got := title(t); got != "" {
		t.Fatalf("forget left the borrowed title in place: %q", got)
	}
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:s11"); err != nil {
		t.Fatal(err)
	}
	if got := title(t); !strings.Contains(got, "pool cap") {
		t.Errorf("the note did not get its title back: %q", got)
	}
}
