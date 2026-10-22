package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func claudeHistoryLine(t *testing.T, id, cwd, display string, at time.Time, pasted map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"display": display, "pastedContents": pasted, "timestamp": at.UnixMilli(),
		"project": cwd, "sessionId": id,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// Claude Code's cleanup deletes transcripts and leaves history.jsonl: the
// prompts of a deleted session come back from it, and a session that still
// has its transcript is left to the transcript (#5046).
func TestClaudeHistoryReadsOnlySessionsWithoutATranscript(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "projects")
	hist := filepath.Join(tmp, "history.jsonl")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CLAUDE_HISTORY_FILE", hist)
	cwd := filepath.Join(tmp, "work", "pool")
	live := filepath.Join(root, claudeEncodedDir(cwd), "live-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	body := claudeHistoryLine(t, "live-1", cwd, "a prompt the transcript holds", at, nil) +
		claudeHistoryLine(t, "gone-2", cwd, "why does the pool leak [Pasted text #1 +3 lines]", at.Add(time.Minute),
			map[string]any{"1": map[string]any{"id": 1, "type": "text", "content": "pool.go:42 conn never closed"}}) +
		claudeHistoryLine(t, "gone-2", cwd, "and the retry", at.Add(2*time.Minute),
			map[string]any{"7": map[string]any{"id": 7, "type": "text", "contentHash": "abc"}, "3": map[string]any{"id": 3, "type": "text", "content": "a paste the line does not name"}}) +
		claudeHistoryLine(t, "gone-3", "", "   ", at, nil) +
		"not json\n"
	if err := os.WriteFile(hist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeHistory(hist)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %+v, want only gone-2", ss)
	}
	s := ss[0]
	if s.ID != "gone-2" || s.Harness != "claude" || s.Path != hist || s.Project != "work/pool" {
		t.Fatalf("session = %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %+v", s.Messages)
	}
	if got := s.Messages[0]; got.Role != "user" || got.Text != "why does the pool leak pool.go:42 conn never closed" || !got.Time.Equal(at.Add(time.Minute)) {
		t.Errorf("first prompt = %+v, want the paste put back where the line names it", got)
	}
	if got := s.Messages[1].Text; got != "and the retry\n\na paste the line does not name" {
		t.Errorf("second prompt = %q, want the unnamed paste after it and the hashed one left out", got)
	}
	if !IsClaudeHistory(hist) || IsClaudeHistory(live) || !IsPromptLog(hist) {
		t.Error("the prompt log is not told apart from a transcript")
	}
	if k := KindForPath(hist); k != "claude-history" {
		t.Errorf("KindForPath(history) = %q", k)
	}
	if k := KindForPath(live); k != "claude" {
		t.Errorf("KindForPath(transcript) = %q", k)
	}

	// The full load: the transcript's own session, and the prompts of the
	// one that is gone.
	if err := os.WriteFile(live, []byte(`{"type":"user","sessionId":"live-1","timestamp":"2026-03-01T09:00:00Z","cwd":"`+filepath.ToSlash(cwd)+`","message":{"role":"user","content":"a prompt the transcript holds"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := map[string]model.Session{}
	for _, s := range LoadClaude() {
		ids[s.ID] = s
	}
	if len(ids) != 2 || ids["live-1"].Path != live || ids["gone-2"].Path != hist {
		t.Errorf("LoadClaude = %+v", ids)
	}
	files := ClaudeFiles()
	if len(files) != 2 || files[1] != hist {
		t.Errorf("ClaudeFiles = %v, want the transcript and the prompt log", files)
	}
}

// A stand pins its store with DEJA_CLAUDE_ROOT; the machine's prompt log
// beside the real ~/.claude stays out of it unless the stand names one.
func TestClaudeHistoryStaysOutOfAPinnedStore(t *testing.T) {
	t.Setenv("DEJA_CLAUDE_HISTORY_FILE", "")
	t.Setenv("DEJA_CLAUDE_ROOT", t.TempDir())
	if got := ClaudeHistoryFile(); got != "" {
		t.Errorf("ClaudeHistoryFile = %q with the root pinned", got)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/x/cfg")
	if got := ClaudeHistoryFile(); got != filepath.FromSlash("/x/cfg/history.jsonl") {
		t.Errorf("ClaudeHistoryFile = %q", got)
	}
}

func TestClaudeEncodedDir(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/a/my.app": "-Users-a-my-app",
		"/tmp/café":       "-tmp-caf-",
		"/tmp/😀x":         "-tmp---x",
		"":                "",
	} {
		if got := claudeEncodedDir(in); got != want {
			t.Errorf("claudeEncodedDir(%q) = %q, want %q", in, got, want)
		}
	}
	long := "/" + strings.Repeat("a", 250)
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", tmp)
	enc := claudeEncodedDir(long)
	dir := filepath.Join(tmp, enc[:claudeDirNameMax]+"-1x2y3z")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !claudeTranscriptOnDisk("s", long) {
		t.Error("a transcript in a cut, hash-suffixed folder was not found")
	}
	if claudeTranscriptOnDisk("t", long) {
		t.Error("a session with no transcript was found")
	}
}

// The ids a prompt log's lines name between two offsets, in either client's
// field, and nothing for a line that does not decode (#5403).
func TestPromptLogSessionIDsReadsOnlyTheSpan(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.jsonl")
	head := `{"session_id":"a","text":"one"}` + "\n"
	tail := `{"session_id":"b","text":"two"}` + "\n" + `{"sessionId":"c","display":"three"}` + "\n" + "{torn\n" + `{"session_id":"d","text":"after"}` + "\n"
	if err := os.WriteFile(p, []byte(head+tail), 0o644); err != nil {
		t.Fatal(err)
	}
	end := int64(len(head+tail) - len(`{"session_id":"d","text":"after"}`+"\n"))
	got, err := PromptLogSessionIDs(p, int64(len(head)), end)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["b"] || !got["c"] {
		t.Errorf("ids = %v, want b and c", got)
	}
	if _, err := PromptLogSessionIDs(filepath.Join(t.TempDir(), "none"), 0, 1); err == nil {
		t.Error("a missing log read as empty")
	}
}
