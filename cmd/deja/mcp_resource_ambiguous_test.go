package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every door a person types names an ambiguous prefix before answering from
// one of the matches. The resource read — the door an agent uses — picked the
// newest silently, which is a wrong answer nobody can see (#2388).
func TestTheResourceReadNamesAnAmbiguousPrefix(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"dupe-one", "dupe-two"} {
		at := time.Now().Add(-time.Duration(3+i) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":"local work in %s"}}`, id, at, id)
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: a whole id answers with no such note.
	whole, code, msg := mcpResourceRead(dir, "deja://session/dupe-one")
	if code != 0 {
		t.Fatalf("a whole id was refused: %d %s", code, msg)
	}
	if strings.Contains(resourceText(t, whole), "sessions match") {
		t.Errorf("an unambiguous read carries an ambiguity note")
	}

	got, code, msg := mcpResourceRead(dir, "deja://session/dupe")
	if code != 0 {
		t.Fatalf("an ambiguous prefix was refused rather than answered: %d %s", code, msg)
	}
	text := resourceText(t, got)
	if !strings.Contains(text, "dupe-one") {
		t.Fatalf("the read did not answer from the newest match:\n%s", text)
	}
	note := strings.Index(text, "2 sessions match")
	if note < 0 {
		t.Fatalf("the reader is not told another session matched:\n%s", text)
	}
	// deja's own words, not recalled text: the note belongs above the frame the
	// served transcript is wrapped in.
	if frame := strings.Index(text, "<deja-recall>"); frame >= 0 && note > frame {
		t.Errorf("the note sits inside the untrusted-data frame:\n%s", text)
	}
}
