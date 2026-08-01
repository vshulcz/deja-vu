package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Corrections append, so file order served the oldest one first — and every
// reader takes the first messages. After a hundred careful corrections the
// hook handed the agent the first answer as fact (#812).
func TestPromotedNoteServesTheLatestCorrectionFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	var lines []string
	for i := 1; i <= 5; i++ {
		row := map[string]any{
			"kind": "promoted", "session": "claude:c1", "project": "p/proj",
			"title": "davit setpoint", "text": "correction " + strconv.Itoa(i),
			"src": "claude", "state": "accepted",
			"ts": base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
		}
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	// A plain note in the same file keeps its own order: it is a day's log,
	// not a decision superseded by the next line.
	plain, err := json.Marshal(map[string]any{
		"text": "plain first", "src": "claude", "project": "p/proj",
		"ts": base.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	plain2, err := json.Marshal(map[string]any{
		"text": "plain second", "src": "claude", "project": "p/proj",
		"ts": base.Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, string(plain), string(plain2))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ss, err := ParseNotesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var note, log *sessionRef
	for i := range ss {
		if strings.HasPrefix(ss[i].ID, "deja-note-") {
			note = &sessionRef{ss[i].ID, ss[i].Messages}
		} else {
			log = &sessionRef{ss[i].ID, ss[i].Messages}
		}
	}
	if note == nil || len(note.msgs) != 5 {
		t.Fatalf("promoted note: %+v", note)
	}
	if !strings.Contains(note.msgs[0].Text, "correction 5") {
		t.Errorf("first message is %q, want the latest correction", note.msgs[0].Text)
	}
	if !strings.Contains(note.msgs[4].Text, "correction 1") {
		t.Errorf("last message is %q, want the first correction", note.msgs[4].Text)
	}
	if log == nil || len(log.msgs) != 2 {
		t.Fatalf("plain notes: %+v", log)
	}
	if !strings.Contains(log.msgs[0].Text, "plain first") {
		t.Errorf("plain notes were reordered: %q", log.msgs[0].Text)
	}
}

type sessionRef struct {
	id   string
	msgs []model.Message
}
