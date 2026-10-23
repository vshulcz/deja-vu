package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// MiniMax Code 0.6.12 hands every hook a transcript_path, but it is a
// temporary Claude-compatible projection under $TMPDIR/minimax-plugin-hooks,
// deleted after the call. The hook entry says --harness mcode, and the
// session's own messages.jsonl is found by the id (#5593). The workspace
// matched against the hook's cwd is workspace_dir, the worktree, not the
// parent repository a worktree session is filed under.
func TestMcodeCompactionReadsTheStoreNotTheTemporaryProjection(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "mvs_5f6e7293aeb14dcdb706331fb8c9cd36"
	session := filepath.Join(sources.McodeRoot(), "2026", "10", "22", "15-47-55-700-session_bXZzXzVmNmU3MjkzYWViMTRkY2RiNzA2MzMxZmI4YzljZDM2")
	compactionWrite(t, filepath.Join(session, "manifest.json"), `{"schemaVersion":1,"sessionId":"`+id+`"}`)
	compactionWrite(t, filepath.Join(session, "messages.jsonl"), strings.Join([]string{
		`{"message_id":"m1","turn_id":"t1","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>\n\nfix the parser test"}],"timestamp":1792684076371,"canonicalTextRange":{"startOffset":38,"endOffset":57}}}`,
		`{"message_id":"m2","turn_id":"t1","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"timestamp":1792684076500}}`,
	}, "\n")+"\n")
	// The workspace is in the runtime's own database, not in the transcript.
	db := sources.McodeStateDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := "create table local_runtime_sessions (session_id text primary key, workspace_dir text, project_workspace_dir text, is_default_workspace integer not null default 0);" +
		"insert into local_runtime_sessions values ('" + id + "', '" + strings.ReplaceAll(workspace, "'", "''") + "', '/elsewhere/parent-repo', 0);"
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	// The projection the runtime would name: not a store deja reads.
	projection := filepath.Join(t.TempDir(), "8c72ac06e04547e3593e556b37863f0c.compatible.jsonl")
	if err := os.WriteFile(projection, []byte(`{"type":"user","message":{"role":"user","content":"other words"},"sessionId":"`+id+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := precompactThen(t, dir, workspace, map[string]any{
		"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace,
		"trigger": "auto", "transcript_path": projection,
	}, "mcode")
	if !strings.Contains(out, "Compaction context") || !strings.Contains(out, "fix the parser test") || strings.Contains(out, "other words") {
		t.Fatalf("packet = %s, want the store's session", out)
	}
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "mcode" {
		t.Errorf("the capture is filed under %q, want mcode", state.Harness)
	}
}
