package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The Notes tab named `deja remember` in its own sentence and was built from
// promoted records only, so the one place a remembered note is named is the
// one place it does not appear (#2403).
func TestTheNotesTabPromisesOnlyWhatItHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	line := `{"type":"user","sessionId":"plain1","timestamp":"` + at + `","cwd":"/work/app",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "plain1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	remembered := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"work/app",` +
		`"text":"keep the retry budget at 3"}`
	if err := os.WriteFile(notes, []byte(remembered+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)

	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var carried []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &carried); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	holdsIt := false
	for _, n := range carried {
		if text, _ := n["text"].(string); strings.Contains(text, "retry budget at 3") {
			holdsIt = true
		}
	}
	// The sentence under the tab is a promise about what is above it: name
	// `deja remember` there and either its notes are on the tab, or the
	// sentence says where they are instead.
	tab := regexp.MustCompile(`(?s)<div id="tab-notes".*?</p>`).FindString(page)
	if tab == "" {
		t.Fatalf("the notes tab is no longer shaped as one block with a note under it")
	}
	if !holdsIt && strings.Contains(tab, "deja remember") && !strings.Contains(tab, "Sessions tab") {
		t.Errorf("the tab names `deja remember`, carries none of its notes, and does not say where they are:\n%s", tab)
	}
}
