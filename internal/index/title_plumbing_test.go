package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// #1100 named the tool-only session so it would stop listing as an empty
// bracket. A session whose every turn is harness plumbing — a CLI's own
// stdout, a task notification — fell off the end of the same chain and listed
// as one: on this machine's index of 1,670, one row is a bracket and an id and
// nothing else. The surfaces that try to fill it in call firstUserTitle, which
// reads .Messages, and they are all fed by Recent, which returns metadata only
// (#2548).
func TestAPlumbingOnlySessionIsStillNamed(t *testing.T) {
	at := time.Date(2026, 8, 11, 15, 48, 0, 0, time.UTC)
	s := model.Session{ID: "plumbing", Harness: "claude", Project: "proj", Updated: at, Messages: []model.Message{
		{Role: "user", Text: "<local-command-stdout>That session is still running as a background agent. Open `claude agents` to attach.</local-command-stdout>", Time: at},
	}}
	meta := metaForSession(s)
	if meta.Title == "" {
		t.Fatal("the session lists as a bare id")
	}
	if want := "harness output: That session is still running as a backgroun…"; meta.Title != want {
		t.Errorf("title = %q, want %q", meta.Title, want)
	}
	// Not the reader's question and not the agent's words.
	if meta.AgentTitle {
		t.Error("harness plumbing was marked as the agent's own words")
	}
	// A session that has something better to say is unaffected.
	s.Messages = append(s.Messages, model.Message{Role: "user", Text: "why does the pool run out under load?", Time: at.Add(time.Minute)})
	if meta := metaForSession(s); meta.Title != "why does the pool run out under load?" {
		t.Errorf("a real turn lost its title: %q", meta.Title)
	}
}

// The receiving machine derives the title from the record stream alone, so the
// two paths have to agree — the same pairing #1100 keeps for the tool-only
// session.
func TestImportNamesAPlumbingOnlySessionTheSameWay(t *testing.T) {
	dir := t.TempDir()
	exp := filepath.Join(dir, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 11, 15, 48, 0, 0, time.UTC)
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "plumbing", Project: "proj",
		Role: "user", Text: "<local-command-stdout>That session is still running as a background agent.</local-command-stdout>", Time: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(string(b)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(dir, "index"), exp); err != nil {
		t.Fatal(err)
	}
	ss, err := Recent(filepath.Join(dir, "index"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("imported %d sessions, want 1", len(ss))
	}
	if !strings.HasPrefix(ss[0].Title, "harness output: That session is still running") {
		t.Errorf("imported title = %q, want the name the local path gives", ss[0].Title)
	}
	if ss[0].AgentTitle {
		t.Error("imported plumbing was marked as the agent's own words")
	}
}
