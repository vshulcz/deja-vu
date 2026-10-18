package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// DEJA_INCLUDE_SUBAGENTS=1 took Claude, Cursor, gjc, prime, senpi and Kimchi
// sub-agent runs, and did nothing for Kimi Code or Qwen Code: their file lists
// kept only the main transcript (#4483). Under the switch the run is read as a
// sub-agent of the session it sits under; without it, it stays out.
func TestKimiAndQwenSubagentsComeInUnderTheSwitch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kimiSession := filepath.Join(root, "kimi", "sessions", "wd_proj_ab", "session_t01")
	put(filepath.Join(kimiSession, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","updatedAt":"2026-07-01T10:00:05.000Z","title":"parent task","workDir":"/tmp/proj"}`)
	put(filepath.Join(kimiSession, "agents", "main", "wire.jsonl"), kimiWireHead)
	kimiChild := put(filepath.Join(kimiSession, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"trace the kimichild backoff jitter"}]},"time":1782295203000}
`)
	qwenProject := filepath.Join(root, "qwen", "projects", "-tmp-proj")
	put(filepath.Join(qwenProject, "chats", "q-1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"parent question"}]}}`+"\n")
	qwenChild := put(filepath.Join(qwenProject, "subagents", "q-1", "agent-a1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:05:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"trace the qwenchild backoff jitter"}]}}`+"\n")

	has := func(files []string, p string) bool {
		for _, f := range files {
			if f == p {
				return true
			}
		}
		return false
	}
	// Both are read by default now (#5223, #5229); =0 keeps them out.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if has(KimiSessionFiles(), kimiChild) {
		t.Fatal("a Kimi sub-agent log is read under DEJA_INCLUDE_SUBAGENTS=0")
	}
	if has(QwenSessionFiles(), qwenChild) {
		t.Fatal("a Qwen sub-agent log is read under DEJA_INCLUDE_SUBAGENTS=0")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	for _, tc := range []struct {
		name, child, parent, word string
		files                     []string
		parse                     func(string) ([]model.Session, error)
		load                      func() []model.Session
	}{
		{"kimi", kimiChild, "session_t01", "kimichild", KimiSessionFiles(), ParseKimiFile, LoadKimi},
		{"qwen", qwenChild, "q-1", "qwenchild", QwenSessionFiles(), ParseQwenFile, LoadQwen},
	} {
		if !has(tc.files, tc.child) {
			t.Errorf("%s: DEJA_INCLUDE_SUBAGENTS=1 leaves the sub-agent log out: %v", tc.name, tc.files)
			continue
		}
		ss, err := tc.parse(tc.child)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: parsed %d sessions: %v", tc.name, len(ss), err)
		}
		s := ss[0]
		if s.Kind != "subagent" || s.Parent != tc.parent || s.ID == tc.parent {
			t.Errorf("%s: child read as id=%q kind=%q parent=%q, want a sub-agent of %s under its own id", tc.name, s.ID, s.Kind, s.Parent, tc.parent)
		}
		if len(s.Messages) == 0 || !strings.Contains(s.Messages[0].Text, tc.word) {
			t.Errorf("%s: the child's turn did not come through: %#v", tc.name, s.Messages)
		}
		ids := map[string]bool{}
		for _, s := range tc.load() {
			ids[s.ID] = true
		}
		if len(ids) != 2 {
			t.Errorf("%s: parent and child load as %d distinct sessions, want 2: %v", tc.name, len(ids), ids)
		}
	}
}
