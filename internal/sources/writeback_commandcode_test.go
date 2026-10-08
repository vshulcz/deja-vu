package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// writeBackTurnsOf is the user and assistant turns of ss, in order.
func writeBackTurnsOf(ss []model.Session) []model.Message {
	var out []model.Message
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role == "user" || m.Role == "assistant" {
				out = append(out, model.Message{Role: m.Role, Text: m.Text})
			}
		}
	}
	return out
}

func writeBackSampleTurns() []model.Message {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return []model.Message{
		{Role: "user", Text: "why does the pool exhaust", Time: at},
		{Role: "command", Text: "$ go test ./pool", Time: at.Add(time.Second)},
		{Role: "assistant", Text: "the cap is 10 & the load is 50 <ok>", Time: at.Add(2 * time.Second)},
		{Role: "tool-output", Text: "FAIL", Time: at.Add(3 * time.Second)},
		{Role: "user", Text: "raise it", Time: at.Add(4 * time.Second)},
	}
}

func TestWriteBackCommandCodeRoundTrips(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	id := "f3e3619b-9a5a-4ad3-8f2b-6dedf60d1d4b"
	path := filepath.Join(root, "private-tmp-work-app", id+".jsonl")
	s := model.Session{Harness: "commandcode", ID: id, Path: path, Messages: writeBackSampleTurns()}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != path || f.Root != root || f.Turns != 3 {
		t.Fatalf("got %s under %s with %d turns", f.Path, f.Root, f.Turns)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCommandCodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("parsed %#v", ss)
	}
	got := writeBackTurnsOf(ss)
	want := []model.Message{{Role: "user", Text: "why does the pool exhaust"}, {Role: "assistant", Text: "the cap is 10 & the load is 50 <ok>"}, {Role: "user", Text: "raise it"}}
	if len(got) != len(want) {
		t.Fatalf("turns = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestWriteBackCommandCodeRefusesAPathItDoesNotKnow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	s := model.Session{Harness: "commandcode", ID: "abc", Path: filepath.Join(root, "p", "abc.checkpoints.jsonl"), Messages: writeBackSampleTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("a checkpoints stream was taken for a transcript")
	}
}
