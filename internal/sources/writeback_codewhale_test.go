package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBackCodeWhaleRoundTrips(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_CODEWHALE_ROOT", root)
	t.Setenv("CODEWHALE_HOME", t.TempDir())
	id := "05a3ec91-f208-4981-a1c2-bc29c7878404"
	path := filepath.Join(root, id+".json")
	s := model.Session{Harness: "codewhale", ID: id, Path: path, Messages: writeBackSampleTurns()}
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != path || f.Turns != 3 {
		t.Fatalf("got %s with %d turns", f.Path, f.Turns)
	}
	if err := os.WriteFile(path, f.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodeWhaleFile(path)
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

func TestWriteBackRefusalsNameWhatIsMissing(t *testing.T) {
	for harness, word := range map[string]string{
		"prime":       "directory",
		"antigravity": "protobuf",
		"amp":         "ampcode.com",
	} {
		s := model.Session{Harness: harness, ID: "x", Path: filepath.Join(t.TempDir(), "x.jsonl"), Messages: writeBackSampleTurns()}
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: err = %v, want a refusal", harness, err)
		}
		if !strings.Contains(r.Reason, word) {
			t.Errorf("%s: reason %q does not say %q", harness, r.Reason, word)
		}
	}
}
