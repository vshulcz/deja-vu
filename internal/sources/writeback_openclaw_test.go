package sources

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBackOpenClawReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	const id = "87bed90b-a18b-4d58-90a2-8956a280dbde"
	s := writeBackSample("openclaw", id, filepath.Join(root, "main", "sessions", id+".jsonl"))
	got := writeAndRead(t, s, ParseOpenClawFile)
	assertTurns(t, got)
	f, err := RenderWriteBack(s)
	if err != nil {
		t.Fatal(err)
	}
	if head := strings.SplitN(string(f.Data), "\n", 2)[0]; strings.Contains(head, `"cwd"`) || !strings.Contains(head, `"type":"session"`) {
		t.Errorf("header %s: want a session header with no cwd", head)
	}
}

func TestWriteBackOpenClawRefusesTheDatabaseAndArchives(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	for path, want := range map[string]string{
		filepath.Join(root, "main", "agent", "openclaw-agent.sqlite"):                    "database",
		filepath.Join(root, "main", "sessions", "s1.jsonl.deleted.2026-10-01T10-00-00Z"): "archived",
	} {
		s := writeBackSample("openclaw", "s1", path)
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) || !strings.Contains(r.Reason, want) {
			t.Errorf("%s: got %v, want a reason naming %q", filepath.Base(path), err, want)
		}
	}
}
