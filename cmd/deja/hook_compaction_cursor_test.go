package main

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// cursorChat writes a cursor-agent 2026.09 chat the way the CLI keeps it: the
// transcript under projects/<folder>/agent-transcripts/<id>/<id>.jsonl, which
// records the calls and none of their results, and the chat store under
// chats/<md5 of the cwd>/<id>/ with its meta.json, which has the results.
func cursorChat(t *testing.T, workspace, id string) string {
	t.Helper()
	chat := filepath.Join(sources.CursorCLIRoot(), "chats", fmt.Sprintf("%x", md5.Sum([]byte(workspace))), id)
	meta, _ := json.Marshal(map[string]any{"cwd": workspace})
	compactionWrite(t, filepath.Join(chat, "meta.json"), string(meta))
	fence := "```"
	msgs := []string{
		`{"role":"user","content":[{"type":"text","text":"fix the parser test"}]}`,
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c1","toolName":"Shell","args":{"command":"go test ./parser/...","description":"run the parser tests"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"Shell","result":"Exit code: 1\n\nCommand output:\n\n` + fence + `\n--- FAIL: TestParseSeed (0.00s)\n    parse_test.go:5: want 3, got 4\nFAIL\n` + fence + `\n\nCommand completed in 410 ms."}]}`,
	}
	var sql strings.Builder
	sql.WriteString("create table blobs (id text primary key, data blob); create table meta (key text primary key, value text);\n")
	var root []byte
	for _, m := range msgs {
		sum := sha256.Sum256([]byte(m))
		root = append(root, 0x0a, 32)
		root = append(root, sum[:]...)
		fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", sum, m)
	}
	rootID := sha256.Sum256(root)
	fmt.Fprintf(&sql, "insert into blobs values ('%x', X'%x');\n", rootID, root)
	fmt.Fprintf(&sql, "insert into meta values ('0', '%s');\n", hex.EncodeToString([]byte(fmt.Sprintf(`{"agentId":"a","latestRootBlobId":"%x"}`, rootID))))
	if out, err := exec.Command("sqlite3", filepath.Join(chat, "store.db"), sql.String()).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	folder := strings.TrimPrefix(encodeLikeClaude(workspace), "-")
	path := filepath.Join(sources.CursorCLIRoot(), "projects", folder, "agent-transcripts", id, id+".jsonl")
	compactionWrite(t, path, strings.Join([]string{
		`{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Wednesday, Oct 7, 2026, 1:20 PM (UTC+3)</timestamp>\n<user_query>\nfix the parser test\n</user_query>"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"go test ./parser/...","description":"run the parser tests"}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}]}}`,
		`{"type":"turn_ended","status":"success"}`,
	}, "\n")+"\n")
	return path
}

// cursor-agent's preCompact names the conversation and its transcript, with
// the project in workspace_roots and no cwd. The transcript is Cursor's own
// shape, which the Claude tail reader does not take; the cursor reader does,
// with the results from the chat store. The packet goes out on the next
// beforeSubmitPrompt or preToolUse.
func TestCursorCompactionPacketReachesTheNextPromptAndToolCall(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, next := range []string{"beforeSubmitPrompt", "preToolUse"} {
		t.Run(next, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			const id = "0115271a-06be-4977-be6b-85aa660f5529"
			path := cursorChat(t, workspace, id)

			pre, _ := json.Marshal(map[string]any{
				"conversation_id": id, "generation_id": "g1", "hook_event_name": "preCompact",
				"workspace_roots": []string{workspace}, "transcript_path": path,
				"trigger": "auto", "context_usage_percent": 91, "cursor_version": "2026.09.02-c22c1a3",
			})
			withHookStdin(t, string(pre))
			runHookPrecompact(dir)
			state, found, err := index.Compaction(dir, id, workspace)
			if err != nil || !found {
				t.Fatalf("precompact stored nothing for a Cursor chat: %v", err)
			}
			if state.Harness != "cursor" {
				t.Errorf("the capture is filed under %q, want cursor", state.Harness)
			}

			var out bytes.Buffer
			if next == "beforeSubmitPrompt" {
				b, _ := json.Marshal(map[string]any{
					"conversation_id": id, "hook_event_name": "beforeSubmitPrompt",
					"workspace_roots": []string{workspace}, "prompt": "carry on",
				})
				if err := runHookPrompt(dir, bytes.NewReader(b), &out); err != nil {
					t.Fatal(err)
				}
			} else {
				b, _ := json.Marshal(map[string]any{
					"conversation_id": id, "hook_event_name": "preToolUse", "tool_name": "Shell",
					"tool_input": map[string]any{"command": "go test ./parser/..."}, "workspace_roots": []string{workspace},
				})
				if err := runHookTool(dir, bytes.NewReader(b), &out); err != nil {
					t.Fatal(err)
				}
			}
			wantCompactionPacket(t, out.String())
		})
	}
}
