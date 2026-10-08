package sources

import (
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBackQwenReadsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	id := "4c7a32fa-6dad-4b2e-b23f-0adcfdadd364"
	s := model.Session{Harness: "qwen", ID: id,
		Path: filepath.Join(root, "projects", "-work-app", "chats", id+".jsonl"), Messages: writeBackTestTurns()}
	f := writeBackToDisk(t, s)
	if f.Turns != 3 {
		t.Errorf("turns %d", f.Turns)
	}
	ss, err := ParseQwenFile(f.Path)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("read back %#v, %v", ss, err)
	}
	checkWriteBackTurns(t, ss[0].Messages)
}

func TestWriteBackQwenRefusesASubagentLog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", root)
	s := model.Session{Harness: "qwen", ID: "agent-1-parent", Kind: "subagent",
		Path: filepath.Join(root, "projects", "-work-app", "subagents", "parent", "agent-1.jsonl"), Messages: writeBackTestTurns()}
	if _, err := RenderWriteBack(s); err == nil {
		t.Fatal("wrote a sub-agent log back")
	}
}
