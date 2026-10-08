package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// encodeLikeClaude names a project folder the way Claude Code, Qwen Code and
// pi do: every character outside [A-Za-z0-9] becomes a dash.
func encodeLikeClaude(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// precompactThen runs hook-precompact with payload, as the host sends it, and
// returns what the next prompt answers for the same session.
func precompactThen(t *testing.T, dir, workspace string, payload map[string]any, harnessArg string) string {
	t.Helper()
	b, _ := json.Marshal(payload)
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, harnessArg)
	id, _ := payload["session_id"].(string)
	if id == "" {
		id, _ = payload["sessionId"].(string)
	}
	if id == "" {
		id, _ = payload["conversation_id"].(string)
	}
	if _, found, err := index.Compaction(dir, id, workspace); err != nil || !found {
		t.Fatalf("precompact stored nothing for %s: %v", id, err)
	}
	return hostsPrompt(t, dir, map[string]any{"session_id": id, "cwd": workspace, "prompt": "what next"})
}

func wantCompactionPacket(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
}

// Qwen Code keeps Claude's sessionId but writes a turn as Gemini parts. The
// Claude reader took it and came out with nothing; its own reader, found by the
// transcript the PreCompact payload names, reads the turns.
func TestQwenCompactionIsReadByItsOwnReader(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "6b1135fe-cb54-43e4-baf1-da67b80b22f3"
	cwd, _ := json.Marshal(workspace)
	rec := func(s string) string { return strings.ReplaceAll(s, "@CWD@", string(cwd)) }
	path := filepath.Join(sources.QwenRoot(), "projects", encodeLikeClaude(workspace), "chats", id+".jsonl")
	compactionWrite(t, path, strings.Join([]string{
		rec(`{"uuid":"u1","parentUuid":null,"sessionId":"` + id + `","timestamp":"2026-10-07T09:00:00.000Z","type":"user","cwd":@CWD@,"version":"0.20.0","gitBranch":"main","message":{"role":"user","parts":[{"text":"fix the parser test"}]}}`),
		rec(`{"uuid":"u2","parentUuid":"u1","sessionId":"` + id + `","timestamp":"2026-10-07T09:00:01.000Z","type":"system","cwd":@CWD@,"version":"0.20.0","gitBranch":"main","subtype":"ui_telemetry","systemPayload":{"uiEvent":{"event.name":"qwen-code.api_response"}}}`),
		rec(`{"uuid":"u3","parentUuid":"u2","sessionId":"` + id + `","timestamp":"2026-10-07T09:00:02.000Z","type":"assistant","cwd":@CWD@,"version":"0.20.0","gitBranch":"main","model":"qwen3-coder","message":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"go test ./parser/..."}}}]}}`),
		rec(`{"uuid":"u4","parentUuid":"u3","sessionId":"` + id + `","timestamp":"2026-10-07T09:00:03.000Z","type":"tool_result","cwd":@CWD@,"version":"0.20.0","gitBranch":"main","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"error":"Command: go test ./parser/...\nDirectory: (root)\nOutput: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nError: (none)\nExit Code: 1\nSignal: 0\nProcess Group PGID: 52866"}}}]},"toolCallResult":{"callId":"call_1","status":"error","error":{}}}`),
		rec(`{"uuid":"u5","parentUuid":"u4","sessionId":"` + id + `","timestamp":"2026-10-07T09:00:04.000Z","type":"assistant","cwd":@CWD@,"version":"0.20.0","gitBranch":"main","model":"qwen3-coder","message":{"role":"model","parts":[{"text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}]}}`),
	}, "\n")+"\n")
	// Qwen's PreCompact payload, Claude-shaped.
	out := precompactThen(t, dir, workspace, map[string]any{
		"session_id": id, "transcript_path": path, "cwd": workspace,
		"hook_event_name": "PreCompact", "trigger": "manual",
	}, "")
	wantCompactionPacket(t, out)
	state, _, _ := index.Compaction(dir, id, workspace)
	if state.Harness != "qwen" {
		t.Errorf("the capture is filed under %q, want qwen", state.Harness)
	}
}

// TRAE CLI 0.207 opens its rollout with Codex's session_meta but writes the
// turns as history_mutation records, which the Codex parser does not read: a
// live /compact captured nothing. The rollout goes to TRAE's own reader. The
// fixture is the first turn of a 0.207.1-tob stand rollout, with the command
// and its output swapped for a failing test run.
func TestTraeRolloutCompactionIsReadByTheTraeReader(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "01a115da-1fd5-7b91-ac81-fdc6bc75eb8d"
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "synthetic", "compaction", "trae-0.207.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(workspace)
	rollout := strings.ReplaceAll(string(b), "WORKSPACE_PATH", strings.Trim(string(quoted), `"`))
	if _, err := sources.ReadCompactionTranscript(writeTemp(t, rollout), id); err == nil {
		t.Fatal("the Codex tail reader took a TRAE 0.207 rollout; the fixture no longer shows the case")
	}
	path := filepath.Join(sources.TraeRoot(), "sessions", "2026", "10", "07", "rollout-2026-10-07T13-12-58-"+id+".jsonl")
	compactionWrite(t, path, rollout)
	out := precompactThen(t, dir, workspace, map[string]any{
		"session_id": id, "transcript_path": path, "cwd": workspace, "hook_event_name": "PreCompact", "trigger": "manual",
	}, "")
	wantCompactionPacket(t, out)
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "trae" {
		t.Errorf("the capture is filed under %q, want trae", state.Harness)
	}
	// The packet was stored, so the log must not say it stored nothing: on a
	// TRAE CLI 0.208.1-alpha.5 stand every /compact read "(stored nothing: the
	// transcript ended mid-turn)" while the packet reached the next turn.
	journal, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var capture *usage.Event
	for _, line := range strings.Split(strings.TrimSpace(string(journal)), "\n") {
		var e usage.Event
		if json.Unmarshal([]byte(line), &e) == nil && e.Kind == usage.KindCompactionCapture {
			capture = &e
		}
	}
	if capture == nil {
		t.Fatalf("no compaction_capture event in the journal:\n%s", journal)
	}
	if capture.CompactionError != "no_offsets" {
		t.Errorf("compaction_error = %q, want no_offsets for a store read through its parser", capture.CompactionError)
	}
	if note := compactionFailureNote(*capture); strings.Contains(note, "stored nothing") || !strings.Contains(note, "stored;") {
		t.Errorf("the log row for a stored packet reads %q", note)
	}
}

// The Claude tail reader recognises a Claude transcript only after every
// record: a Qwen one opens with the same envelope.
func TestTheClaudeTailReaderRefusesQwenParts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.jsonl")
	compactionWrite(t, path, `{"sessionId":"q1","type":"system","subtype":"ui_telemetry","cwd":"/w"}`+"\n"+
		`{"sessionId":"q1","type":"user","cwd":"/w","message":{"role":"user","parts":[{"text":"hi"}]}}`+"\n")
	if _, err := sources.ReadCompactionTranscript(path, "q1"); err == nil {
		t.Fatal("a Qwen transcript was read as Claude's")
	}
}

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	compactionWrite(t, path, text)
	return path
}
