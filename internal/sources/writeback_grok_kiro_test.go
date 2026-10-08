package sources

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// gkSession is a kept session as the index holds it: turns, two answers in a
// row where a tool call sat between them, and a last prompt with no reply.
func gkSession(harness, id, path string) model.Session {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return model.Session{
		Harness: harness, ID: id, Path: path, Started: at, Title: "pool question",
		Messages: []model.Message{
			{Role: "user", Text: "why does the pool exhaust", Time: at},
			{Role: "assistant", Text: "checking the pool", Time: at.Add(time.Second)},
			{Role: "command", Text: "$ go test ./pool", Time: at.Add(2 * time.Second)},
			{Role: "assistant", Text: "raise MaxOpenConns to 50", Time: at.Add(3 * time.Second)},
			{Role: "user", Text: "and the timeout?", Time: at.Add(4 * time.Second)},
		},
	}
}

// gkWrite renders s and writes the transcript and the files beside it.
func gkWrite(t *testing.T, s model.Session) (WriteBackFile, []WriteBackPart) {
	t.Helper()
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	sidecars, appends, err := WriteBackExtras(s, f)
	if err != nil || len(appends) != 0 {
		t.Fatalf("extras: %v, %d appends", err, len(appends))
	}
	for _, w := range append([]WriteBackPart{{Path: f.Path, Data: f.Data}}, sidecars...) {
		if writeBackRootOf(w.Path, []string{f.Root}) == "" {
			t.Fatalf("%s is outside %s", w.Path, f.Root)
		}
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(w.Path, w.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f, sidecars
}

func gkTurns(ms []model.Message) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Role+":"+m.Text)
	}
	return strings.Join(out, "|")
}

func TestWriteBackGrokReadsBackWithItsSummary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	id := "01a11b02-2491-7ae0-ba1b-48dd82cc8841"
	path := filepath.Join(root, "sessions", "%2Fwork%2Fapi", id, "updates.jsonl")
	f, side := gkWrite(t, gkSession("grok", id, path))
	ss, err := ParseGrokFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	want := "user:why does the pool exhaust|assistant:checking the pool|assistant:raise MaxOpenConns to 50|user:and the timeout?"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%s\nwant:\n%s", got, want)
	}
	if ss[0].Project != projectName("/work/api") {
		t.Errorf("project %q", ss[0].Project)
	}
	// grok 1.0.41 answers "Session does not exist" unless these are present.
	var doc map[string]any
	if len(side) != 1 || json.Unmarshal(side[0].Data, &doc) != nil {
		t.Fatalf("summary sidecar: %#v", side)
	}
	for _, k := range []string{"info", "session_summary", "created_at", "updated_at", "num_messages", "current_model_id", "chat_format_version"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("summary.json lacks %s", k)
		}
	}
}

func TestWriteBackGrokRefusesTheGrokDevDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	s := gkSession("grok", "abc", filepath.Join(root, "grok.db"))
	var r *WriteBackRefusal
	if _, err := RenderWriteBack(s); !errors.As(err, &r) || !strings.Contains(r.Reason, "grok.db") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteBackKiroCLIAlternatesAndReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(root, "none.sqlite3"))
	id := "3b0e2c6a-1111-4222-8333-444455556666"
	f, side := gkWrite(t, gkSession("kiro", id, filepath.Join(root, "cli", id+".jsonl")))
	ss, err := ParseKiroCLIFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	// kiro-cli refuses two answers in a row and a history that ends on a prompt.
	want := "user:why does the pool exhaust|assistant:checking the pool\n\nraise MaxOpenConns to 50"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%q\nwant:\n%q", got, want)
	}
	if len(side) != 1 || filepath.Base(side[0].Path) != id+".json" {
		t.Fatalf("header sidecar: %#v", side)
	}
}

func TestWriteBackKiroV3ReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", filepath.Join(root, "sessions"))
	t.Setenv("DEJA_KIRO_DB", filepath.Join(root, "none.sqlite3"))
	id := "sess_011ff21a-4e1f-4f50-b3ca-4f40f34a81c4"
	if err := os.MkdirAll(filepath.Join(root, "session-index"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"op":"add","sessionPath":"5faa503d2a4212ce/` + id + `","at":1791455425264}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "session-index", "5faa503d2a4212ce.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	f, side := gkWrite(t, gkSession("kiro", id, filepath.Join(root, "sessions", "5faa503d2a4212ce", id, "messages.jsonl")))
	ss, err := ParseKiroIDEFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	want := "user:why does the pool exhaust|assistant:checking the pool\n\nraise MaxOpenConns to 50"
	if got := gkTurns(ss[0].Messages); got != want {
		t.Errorf("turns:\n%q\nwant:\n%q", got, want)
	}
	if len(side) != 1 || filepath.Base(side[0].Path) != "session.json" {
		t.Fatalf("session.json sidecar: %#v", side)
	}
}

func TestWriteBackKiroRefusesTheIDEAndTheDatabase(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIRO_ROOT", filepath.Join(root, "sessions"))
	db := filepath.Join(root, "data.sqlite3")
	t.Setenv("DEJA_KIRO_DB", db)
	for _, s := range []model.Session{
		gkSession("kiro", "conv-1", db),
		gkSession("kiro", "sess_aa", filepath.Join(root, "sessions", "ws", "sess_aa", "messages.jsonl")),
	} {
		var r *WriteBackRefusal
		if _, err := RenderWriteBack(s); !errors.As(err, &r) || r.Reason == "" {
			t.Errorf("%s: err = %v", s.Path, err)
		}
	}
}

func TestWriteBackCursorSaysWhatIsMissing(t *testing.T) {
	var r *WriteBackRefusal
	_, err := RenderWriteBack(gkSession("cursor", "c1", "/x/agent-transcripts/c1.jsonl"))
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "store.db") {
		t.Fatalf("err = %v", err)
	}
}
