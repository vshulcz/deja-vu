package index

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// allHarnessEnv points every source at a sandbox subdir and returns the
// index dir. Missing stores are simply empty.
func allHarnessEnv(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(root, "opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(root, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(root, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(root, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(root, "aiderroot"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(root, "grok"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "index.db")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedFileHarnesses(t *testing.T, root string) {
	// claude
	write(t, filepath.Join(root, "claude", "-p-app", "c1.jsonl"),
		`{"type":"user","sessionId":"c1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"claundneedle unicode тест 日本語"}}`+"\n")
	// codex rollout + history
	write(t, filepath.Join(root, "codex", "sessions", "2026", "02", "01", "rollout-2026-02-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-02-01T10:00:00Z","payload":{"session_id":"x1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-02-01T10:00:01Z","payload":{"role":"user","content":"codexneedle question"}}`+"\n")
	write(t, filepath.Join(root, "codex", "history.jsonl"),
		`{"session_id":"h1","ts":1769940000,"text":"historyneedle entry"}`+"\n")
	// gemini dual format, same session id
	gj := `{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"geminineedle dup"}]}`
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.json"), gj)
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.jsonl"),
		`{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:05:00.000Z"}`+"\n"+
			`{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"geminineedle dup"}`+"\n")
	// antigravity transcript
	write(t, filepath.Join(root, "antigravity", "brain", "traj-1", ".system_generated", "logs", "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-02-01T10:00:00Z","content":"<USER_REQUEST>\nantineedle request\n</USER_REQUEST>"}`+"\n"+
			`{"step_index":1,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","created_at":"2026-02-01T10:00:01Z","content":"noise skipped"}`+"\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-02-01T10:00:02Z","content":"antineedle answer","thinking":"hidden"}`+"\n")
	// aider
	write(t, filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
		"# aider chat started at 2026-02-01 10:00:00\n\n#### aiderneedle question\n\naiderneedle answer\n")
	// grok
	grokDir := filepath.Join(root, "grok", "sessions", "%2Fp%2Fapp")
	write(t, filepath.Join(grokDir, "gr1", "summary.json"),
		`{"info":{"id":"gr1","cwd":"/p/app"},"generated_title":"grok session","created_at":"2026-02-01T10:00:00Z","updated_at":"2026-02-01T10:05:00Z"}`)
	write(t, filepath.Join(grokDir, "gr1", "updates.jsonl"),
		`{"timestamp":1769940000000,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"grokneedle question"},"_meta":{"promptIndex":0}},"_meta":{"promptId":"p0"}}}`+"\n")
}

func seedCursorDB(t *testing.T, root string) bool {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return false
	}
	db := filepath.Join(root, "cursor-user", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:cu1', json('{"composerId":"cu1","name":"cursortitle","createdAt":1769940000000,"lastUpdatedAt":1769940300000}')),
 ('bubbleId:cu1:b1', json('{"type":1,"text":"cursorneedle question","timestamp":1769940001000,"workspaceProjectDir":"/p/app"}')),
 ('bubbleId:cu1:b2', json('{"type":2,"text":"cursorneedle answer","timestamp":1769940002000}'));`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("seed cursor db: %v: %s", err, out)
	}
	return true
}

// The regression that keeps biting: every harness must index, search, and
// carry exactly one copy of each message through a full build.
func TestAllHarnessesIndexAndSearch(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	hasCursor := seedCursorDB(t, root)

	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"claundneedle unicode": "claude", "codexneedle question": "codex",
		"historyneedle entry": "codex", "geminineedle dup": "gemini",
		"antineedle request": "antigravity", "aiderneedle question": "aider",
		"grokneedle question": "grok",
	}
	if hasCursor {
		cases["cursorneedle question"] = "cursor"
	}
	for needle, harness := range cases {
		ss, err := Search(dir, search.Options{Query: needle, All: true})
		if err != nil {
			t.Fatalf("%s: %v", needle, err)
		}
		total := 0
		for _, s := range ss {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, needle) {
					total++
				}
			}
			if s.Harness != harness && len(ss) == 1 {
				t.Fatalf("%s: harness=%s want %s", needle, s.Harness, harness)
			}
		}
		if total != 1 {
			t.Fatalf("%s: %d copies, want exactly 1", needle, total)
		}
	}
	// SYSTEM step and gemini thinking must not be indexed
	if ss, _ := Search(dir, search.Options{Query: "noise skipped", All: true}); len(ss) != 0 {
		t.Fatal("antigravity SYSTEM step indexed")
	}
	if ss, _ := Search(dir, search.Options{Query: "hidden", All: true}); len(ss) != 0 {
		t.Fatal("antigravity thinking indexed")
	}
}

// Determinism: two from-scratch rebuilds on identical fixtures give identical
// session and message counts.
func TestRebuildDeterminism(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	count := func() (int, int) {
		if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		msgs := 0
		_ = eachRecord(filepath.Join(dir, "records.bin"), func(r Record) { msgs++ })
		return len(m.Sessions), msgs
	}
	s1, m1 := count()
	s2, m2 := count()
	if s1 != s2 || m1 != m2 {
		t.Fatalf("non-deterministic: %d/%d vs %d/%d", s1, m1, s2, m2)
	}
}

// Incremental append to each file-based harness: the new message is found
// and nothing is duplicated.
func TestIncrementalPerHarnessNoDup(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	appends := []struct{ path, line string }{
		{filepath.Join(root, "claude", "-p-app", "c1.jsonl"),
			`{"type":"user","sessionId":"c1","timestamp":"2026-02-01T11:00:00Z","message":{"role":"user","content":"incrneedle claude"}}` + "\n"},
		{filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
			"\n#### incrneedle aider\n\nmore\n"},
	}
	for _, a := range appends {
		time.Sleep(10 * time.Millisecond)
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(a.line); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	if err := EnsureForSearch(dir, search.Options{Query: "incrneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"incrneedle claude", "incrneedle aider"} {
		ss, _ := Search(dir, search.Options{Query: needle, All: true})
		n := 0
		for _, s := range ss {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, needle) {
					n++
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s appeared %d times, want 1", needle, n)
		}
	}
}

// Sync roundtrip across the full harness set: export, import elsewhere,
// re-export must not echo, and search results match.
func TestSyncRoundtripAllHarnesses(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(root, "batch")
	n, err := ExportFull(dir, batch)
	if err != nil || n == 0 {
		t.Fatalf("export n=%d err=%v", n, err)
	}
	dir2 := filepath.Join(root, "index2.db")
	imp, err := Import(dir2, batch)
	if err != nil || imp != n {
		t.Fatalf("import %d, exported %d, err=%v", imp, n, err)
	}
	// re-export from the importing index must not echo imported records
	echo, err := ExportFull(dir2, filepath.Join(root, "batch2"))
	if err != nil {
		t.Fatal(err)
	}
	if echo != 0 {
		t.Fatalf("re-export echoed %d records", echo)
	}
	for _, needle := range []string{"claundneedle", "aiderneedle", "antineedle"} {
		ss, _ := Search(dir2, search.Options{Query: needle, All: true})
		if len(ss) == 0 {
			t.Fatalf("%s missing after sync import", needle)
		}
	}
	// re-import is a no-op
	if again, _ := Import(dir2, batch); again != 0 {
		t.Fatalf("re-import added %d", again)
	}
}

var _ = json.Marshal
