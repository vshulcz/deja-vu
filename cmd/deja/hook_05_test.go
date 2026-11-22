package main

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Cursor names chat dirs by the md5 of the workspace
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/gitread"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAllHookInputsAdoptCamelCaseEnvelopeUniformly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		wantSID string
	}{
		{
			name:    "grok-sessionId",
			payload: `{"sessionId":"grok-s1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "grok-s1",
		},
		{
			name:    "cursor-conversation_id",
			payload: `{"conversation_id":"cur-c1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "cur-c1",
		},
		{
			name:    "antigravity-conversationId",
			payload: `{"conversationId":"agy-a1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "agy-a1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pre precompactHookInput
			if err := json.Unmarshal([]byte(tc.payload), &pre); err != nil {
				t.Fatal(err)
			}
			pre.adopt()

			var prm promptHookInput
			if err := json.Unmarshal([]byte(tc.payload), &prm); err != nil {
				t.Fatal(err)
			}
			prm.adopt()

			var tl toolHookInput
			if err := json.Unmarshal([]byte(tc.payload), &tl); err != nil {
				t.Fatal(err)
			}
			tl.adopt()

			for label, got := range map[string]struct {
				sid, tp string
				roots   []string
			}{
				"precompact": {pre.SessionID, pre.TranscriptPath, pre.WorkspaceRoots},
				"prompt":     {prm.SessionID, prm.TranscriptPath, prm.WorkspaceRoots},
				"tool":       {tl.SessionID, tl.TranscriptPath, tl.WorkspaceRoots},
			} {
				if got.sid != tc.wantSID {
					t.Errorf("%s SessionID = %q, want %q", label, got.sid, tc.wantSID)
				}
				if got.tp != "/tmp/grok.jsonl" {
					t.Errorf("%s TranscriptPath = %q, want /tmp/grok.jsonl", label, got.tp)
				}
				if len(got.roots) != 1 || got.roots[0] != "/tmp/proj" {
					t.Errorf("%s WorkspaceRoots = %v, want [/tmp/proj]", label, got.roots)
				}
			}
		})
	}
}

func TestSessionsHoldingHandlesUTF8PunctuationYoFoldAndWorkRecords(t *testing.T) {
	hits := []search.Hit{
		{
			Session: model.Session{
				ID: "s1",
				Messages: []model.Message{
					{Role: "user", Text: "Why did “retries” stall—and «счётчика» overflow…"},
				},
			},
		},
		{
			Session: model.Session{
				ID: "s2",
				Messages: []model.Message{
					{Role: "tool-output", Text: "retries in tool output should not count as spoken"},
				},
			},
		},
	}

	if got := sessionsHolding(hits, "retries"); got != 1 {
		t.Errorf("sessionsHolding(retries) = %d, want 1 (s1 spoken with UTF-8 quotes/dash, s2 tool-output ignored)", got)
	}
	if got := sessionsHolding(hits, "счетчика"); got != 1 {
		t.Errorf("sessionsHolding(счетчика) = %d, want 1 (s1 with ё->е fold inside «guillemets»)", got)
	}
}

func TestTextCarriesTermHandlesUnicodePunctuationAndExtendedLatin(t *testing.T) {
	if !search.TextCarriesTerm("We switched —retry— and “токен”…", "retry") {
		t.Error("TextCarriesTerm failed to match 'retry' bounded by em-dashes (U+2014)")
	}
	if !search.TextCarriesTerm("Проверь «токен»—он истёк…", "токен") {
		t.Error("TextCarriesTerm failed to match 'токен' bounded by guillemets and em-dash")
	}
	if search.TextCarriesTerm("retrying uberalles", "über") {
		t.Error("TextCarriesTerm matched 'über' when not present")
	}
	if !search.TextCarriesTerm("configured überproxy for local dev", "überproxy") {
		t.Error("TextCarriesTerm failed to match extended-Latin term 'überproxy'")
	}
}

// The lookup behind this line is machine-wide on purpose — an error signature
// is closer to an environment fact than to project history. The sentence said
// "here", which reads as this project, while offering a command from another
// one (#2363).
func TestTheAfterErrorLineSaysWhoseCommandItIs(t *testing.T) {
	when := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	other := fixLine(index.FixPair{
		Error: "panic: quaxbolt overflow", Command: "make acme-widen-cast",
		Key: "claude:acme0", When: when, Project: "clients/acme/api",
	}, 1)
	if other == "" {
		t.Fatal("no line for a usable pair, so this measures nothing")
	}
	if strings.Contains(other, "came up here before") {
		t.Errorf("the line still calls another project's history this one's: %q", other)
	}
	if !strings.Contains(other, "clients/acme/api") {
		t.Errorf("the line does not say whose command it offers: %q", other)
	}
	if !strings.Contains(other, "make acme-widen-cast") {
		t.Errorf("the command is gone from the line: %q", other)
	}

	// A pair mined before the project field existed still gets a line — it just
	// cannot name a project.
	old := fixLine(index.FixPair{
		Error: "panic: quaxbolt overflow", Command: "make widen-cast",
		Key: "claude:s0", When: when,
	}, 1)
	if old == "" || !strings.Contains(old, "make widen-cast") {
		t.Errorf("a pair without a project lost its line: %q", old)
	}
}

// Antigravity compacts without an event of its own: it truncates the
// conversation and writes a CHECKPOINT step saying the earlier parts are gone.
// Until that is noticed, the blocks are missing from the context while the
// record of having sent them outlives them — the one state where recall stays
// silent about exactly what the agent no longer has.
func TestACompactedConversationIsToldAgain(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "cpapp", "sess-cp",
		"why does the packer refuse the manifest",
		"it refused until glimcheckneedle was set in the environment")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	write := func(steps ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	question := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the packer refuse the manifest\n</USER_REQUEST>"}`
	write(question)

	call := func(n int) string {
		payload, err := json.Marshal(map[string]any{
			"invocationNum":  n,
			"conversationId": "conv-cp",
			"transcriptPath": transcript,
			"workspacePaths": []string{filepath.Join(os.TempDir(), "cpapp")},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		var resp antigravityHookResponse
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
			t.Fatalf("the hook did not answer with JSON: %q", out.String())
		}
		if len(resp.InjectSteps) == 0 {
			return ""
		}
		return resp.InjectSteps[0].EphemeralMessage
	}

	if first := call(0); !strings.Contains(first, antigravityLead) {
		t.Fatalf("the conversation did not open with the digest: %q", first)
	}
	// Said once. What comes back on the next call is the question's own
	// answer, which is a different block and the right one to send.
	if again := call(0); strings.Contains(again, antigravityLead) {
		t.Fatalf("the digest went in twice without a compaction: %q", again)
	}

	// Now antigravity truncates the conversation and says so.
	write(question,
		`{"step_index":1,"type":"CHECKPOINT","source":"SYSTEM","created_at":"2026-09-04T12:00:00Z","content":"{{ CHECKPOINT 0 }}\n**The earlier parts of this conversation have been truncated due to its long length.**"}`)

	if after := call(0); !strings.Contains(after, antigravityLead) {
		t.Errorf("what the compaction threw away was not offered again: %q", after)
	}
	// And one compaction is one repeat: the marker is remembered, so the
	// digest does not come back on every call after it.
	if again := call(0); strings.Contains(again, antigravityLead) {
		t.Errorf("the same compaction was answered twice: %q", again)
	}
}

// The marker itself: a conversation with no checkpoint has nothing to forget,
// and the same checkpoint is only acted on once.
func TestACheckpointIsActedOnOnce(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if forgetOnCheckpoint(dir, "conv-1", "") {
		t.Error("a conversation that has not been compacted was treated as if it had")
	}
	if forgetOnCheckpoint(dir, "", "2026-09-04T12:00:00Z") {
		t.Error("a payload with no conversation id was acted on")
	}
	if !forgetOnCheckpoint(dir, "conv-1", "2026-09-04T12:00:00Z") {
		t.Fatal("the first compaction was not acted on")
	}
	if forgetOnCheckpoint(dir, "conv-1", "2026-09-04T12:00:00Z") {
		t.Error("the same compaction was acted on twice")
	}
	// A later compaction in the same conversation is a new one.
	if !forgetOnCheckpoint(dir, "conv-1", "2026-09-04T13:00:00Z") {
		t.Error("a second compaction was mistaken for the first")
	}
}

// Antigravity has no compaction event. It writes a CHECKPOINT step and keeps
// the steps before it, so the next PreInvocation finds the compaction in the
// transcript, captures the conversation as it stood, and hands the packet back
// once, the way Gemini's next BeforeAgent does.
func TestAntigravityCompactionIsCaughtUpAtTheNextInvocation(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	logs := filepath.Join(t.TempDir(), "brain", "agy-1", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(logs, "transcript.jsonl")
	steps := []string{
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-07T10:00:00Z","content":"<USER_REQUEST>fix the parser test</USER_REQUEST>"}`,
		`{"source":"MODEL","type":"RUN_COMMAND","created_at":"2026-10-07T10:00:01Z","content":"Task Description: go test ./parser/...\nOutput:\n--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:00:02Z","content":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}`,
	}
	write := func(extra ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(append(append([]string{}, steps...), extra...), "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func() string {
		payload := mustMarshal(t, map[string]any{
			"invocationNum": 1, "workspacePaths": []string{workspace},
			"transcriptPath": transcript, "conversationId": "agy-1",
		})
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	write()
	if out := invoke(); strings.Contains(out, "Compaction context") {
		t.Fatalf("a conversation that never compacted got a packet: %s", out)
	}
	write(
		`{"source":"SYSTEM","type":"CHECKPOINT","created_at":"2026-10-07T10:05:00Z","content":"summary of the work so far"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:01Z","content":"carrying on"}`,
	)
	out := invoke()
	for _, want := range []string{"Compaction context", "go test ./parser/...", "fix the parser test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "carrying on") {
		t.Errorf("the packet was built from steps after the checkpoint: %s", out)
	}
	if again := invoke(); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
	editPath := filepath.ToSlash(filepath.Join(workspace, "parser", "parse.go"))
	write(
		`{"source":"SYSTEM","type":"CHECKPOINT","created_at":"2026-10-07T10:05:00Z","content":"summary of the work so far"}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:01Z","content":"reading file","tool_calls":[{"name":"read_file","args":{"Path":"`+editPath+`"}}]}`,
		`{"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-07T10:05:02Z","content":"fixing file","tool_calls":[{"name":"edit_file","args":{"Path":"`+editPath+`","Target":"a","Replacement":"b"}}]}`,
	)
	_ = invoke()
	sum := usage.CompactionRecoverySummary(dir)
	if sum == nil || sum.Measured != 1 || sum.MedianActions != 1 {
		t.Fatalf("expected 1 measured Antigravity compaction recovery with 1 action before edit, got %+v", sum)
	}
}

func TestLatestToolFailureFromStepsParsesAntigravityAndMCPFormats(t *testing.T) {
	steps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "Command exited with code 1.\nCommand output:\ninternal/parser/parse.go:42:2: assignment mismatch: 3 variables but ParseToken returns 2 values",
		},
	}
	got := latestToolFailureFromSteps(steps)
	if !strings.Contains(got, "assignment mismatch: 3 variables but ParseToken returns 2 values") {
		t.Fatalf("expected Command exited with code 1 format to extract output, got %q", got)
	}

	mcpSteps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "[cmd direct | exit=1 | 120ms]\n--- FAIL: TestParseToken (0.01s)",
		},
	}
	if gotMCP := latestToolFailureFromSteps(mcpSteps); !strings.Contains(gotMCP, "--- FAIL: TestParseToken") {
		t.Fatalf("expected MCP exit=1 format to extract output, got %q", gotMCP)
	}

	okSteps := []transcriptStep{
		{
			Type:    "GENERIC",
			Content: "Command exited with code 0.\nCommand output:\nPASS",
		},
	}
	if gotOK := latestToolFailureFromSteps(okSteps); gotOK != "" {
		t.Fatalf("expected exit 0 to return empty failure, got %q", gotOK)
	}
}

// Antigravity's PreToolUse reason never reaches the model, so the line the
// pre-tool hook gives before an edit rides the next PreInvocation, read from
// the planner row that made the call — after the edit, with its result.
func TestAntigravityEditLineRidesTheNextInvocation(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i := range 6 {
		id := "s" + string(rune('0'+i))
		writeClaudeFixture(t, filepath.Join(root, "alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"edit"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/alpha","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"We settled on config.go loading the sentinel before any read."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/work/alpha/config.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(tmp, "transcript.jsonl")
	steps := []string{
		`{"type":"USER_INPUT","source":"USER_EXPLICIT","content":"<USER_REQUEST>tidy the loader</USER_REQUEST>","created_at":"2026-10-07T10:00:00Z"}`,
		`{"type":"PLANNER_RESPONSE","source":"MODEL","content":"","created_at":"2026-10-07T10:00:01Z","tool_calls":[{"name":"replace_file_content","args":{"TargetFile":"\"file:///work/alpha/config.go\""}}]}`,
		`{"type":"CODE_ACTION","source":"MODEL","status":"DONE","content":"Edited file:///work/alpha/config.go","created_at":"2026-10-07T10:00:02Z"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := mustMarshal(t, map[string]any{
		"invocationNum": 1, "workspacePaths": []string{"/work/alpha"},
		"transcriptPath": transcript, "conversationId": "agy-1",
	})
	var out bytes.Buffer
	if err := runHookAntigravity(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("reply %q: %v", out.String(), err)
	}
	said := make([]string, 0, len(resp.InjectSteps))
	for _, s := range resp.InjectSteps {
		said = append(said, s.EphemeralMessage)
	}
	if !strings.Contains(strings.Join(said, "\n"), "sentinel") {
		t.Fatalf("the edit's line did not ride the next invocation: %q", said)
	}
	// Once: the next invocation of the same turn says nothing new about it.
	out.Reset()
	if err := runHookAntigravity(os.Getenv("DEJA_INDEX_DIR"), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sentinel") {
		t.Fatalf("the same line came twice: %q", out.String())
	}
}

// Antigravity hands PostToolUse the error and then allows it no answer — its
// contract is an empty object. The moment a command fails is reachable only
// from the invocation that follows it, and the failure is in the transcript:
// a step that says which code the command exited with, and the output under it.
func TestAFailedCommandIsReadFromTheTranscript(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, steps ...string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	failed := write("failed.jsonl",
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nbuild it\n</USER_REQUEST>"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"Created At: now\nCompleted At: now\n\nThe command exited with code 1.\nOutput:\n./main.go:9:2: undefined: glimwraxHelper\n"}`)
	got := latestToolFailure(failed)
	if !strings.Contains(got, "undefined: glimwraxHelper") {
		t.Errorf("the error was not read out of the step: %q", got)
	}
	// The harness's own framing is not part of the error.
	if strings.Contains(got, "Created At") || strings.Contains(got, "exited with code") {
		t.Errorf("the step's framing came back as the error: %q", got)
	}

	// A command that worked is not a failure.
	ok := write("ok.jsonl",
		`{"step_index":0,"type":"GENERIC","status":"DONE","content":"Created At: now\n\nThe command exited with code 0.\nOutput:\nhi\n"}`)
	if got := latestToolFailure(ok); got != "" {
		t.Errorf("a successful command read as a failure: %q", got)
	}

	// And a failure the agent has already moved past is not the moment either:
	// only the newest tool step counts.
	stale := write("stale.jsonl",
		`{"step_index":0,"type":"GENERIC","status":"DONE","content":"The command exited with code 1.\nOutput:\nthe old error\n"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"The command exited with code 0.\nOutput:\nfine now\n"}`)
	if got := latestToolFailure(stale); got != "" {
		t.Errorf("a failure that was already worked past came back: %q", got)
	}

	// Nothing to read is silence, not a guess.
	if got := latestToolFailure(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("a missing transcript answered %q", got)
	}
}

// And the hook speaks it: after a failed command, the next invocation carries
// what this machine ran the last time the same error came up.
func TestTheInvocationAfterAFailureCarriesTheRepair(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaudeFixPair(t, claude, "agyfix")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	steps := []string{
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nbuild it\n</USER_REQUEST>"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"The command exited with code 1.\nOutput:\n./main.go:9:2: undefined: glimwraxHelper\n"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	callAt := func(num int) string {
		payload, merr := json.Marshal(map[string]any{
			"invocationNum":  num,
			"conversationId": "conv-fx",
			"transcriptPath": transcript,
			"workspacePaths": []string{filepath.Join(os.TempDir(), "agyfix")},
		})
		if merr != nil {
			t.Fatal(merr)
		}
		var out bytes.Buffer
		if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// Strike 1 on a non-detEnv confirmed pair stays silent (only opening digest).
	if first := callAt(1); strings.Contains(first, "glimwraxctl") {
		t.Errorf("strike 1 delivered non-detEnv repair too early:\n%s", first)
	}
	// Strike 2 on consecutive failure delivers the repair.
	if second := callAt(2); !strings.Contains(second, "glimwraxctl") {
		t.Errorf("the repair did not arrive at the second failure:\n%s", second)
	}
}

// seedClaudeFixPair writes two sessions that hit the same error and ran the
// command that settled it — confirming the fix pair for auto-hooks.
func seedClaudeFixPair(t *testing.T, root, project string) {
	t.Helper()
	dir := filepath.Join(root, "-"+project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(os.TempDir(), project)
	for _, sid := range []string{"fix1", "fix2"} {
		rec := func(kind string, content any, at string) string {
			b := mustMarshal(t, map[string]any{
				"type": kind, "sessionId": sid, "cwd": cwd, "timestamp": at,
				"message": map[string]any{"role": kind, "content": content},
			})
			return string(b)
		}
		lines := []string{
			rec("user", "the build stops on an undefined symbol", "2026-09-01T10:00:00Z"),
			rec("assistant", []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash",
				"input": map[string]string{"command": "go build ./..."}}}, "2026-09-01T10:00:10Z"),
			rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1",
				"content": "./main.go:9:2: undefined: glimwraxHelper"}}, "2026-09-01T10:00:20Z"),
			rec("assistant", []any{map[string]any{"type": "tool_use", "id": "t2", "name": "Bash",
				"input": map[string]string{"command": "glimwraxctl --sync && go build ./..."}}}, "2026-09-01T10:00:40Z"),
			rec("user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t2",
				"content": "ok"}}, "2026-09-01T10:01:00Z"),
		}
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Antigravity writes each turn into a transcript and hands the hook its path.
// That file is where the question is: the harness has no per-prompt event, so
// without reading it deja can only ever speak once per conversation.
func TestTheNewestQuestionIsReadFromTheTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	lines := []string{
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nthe first question about the toolbar\n</USER_REQUEST>\n"}`,
		`{"step_index":1,"type":"GENERIC","status":"DONE","content":"Created At: whenever"}`,
		`{"step_index":2,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the frobnicator stall\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nlocal time\n</ADDITIONAL_METADATA>\n"}`,
		`{"step_index":3,"type":"EPHEMERAL_MESSAGE","status":"DONE","content":"a block deja injected"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := latestUserRequest(path); got != "why does the frobnicator stall" {
		t.Errorf("latest question = %q", got)
	}
	// The metadata antigravity appends is not the person's words.
	if strings.Contains(latestUserRequest(path), "ADDITIONAL_METADATA") {
		t.Error("the harness's own metadata came back as part of the question")
	}
	// A file that holds no user turn, and one that is not there at all, are
	// both answered with silence rather than with a guess.
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, []byte(`{"step_index":0,"type":"GENERIC","content":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := latestUserRequest(empty); got != "" {
		t.Errorf("a transcript with no question answered %q", got)
	}
	if got := latestUserRequest(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("a missing transcript answered %q", got)
	}
}

// The counter antigravity sends starts at zero and restarts on every turn
// (measured on antigravity-cli 1.1.13). Read as 1-based it put the digest in
// twice per turn, and read as per-conversation it put it in again on every turn
// — so the conversation's own ledger is what decides.
func TestTheDigestLandsOncePerConversation(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const conv = "conv-42"
	if digestAlreadyInjected(dir, conv) {
		t.Fatal("a fresh conversation already counts as served")
	}
	rememberDigestInjected(dir, conv)
	if !digestAlreadyInjected(dir, conv) {
		t.Error("the digest was not recorded, so the next turn sends it again")
	}
	// Another conversation is another reader.
	if digestAlreadyInjected(dir, "conv-43") {
		t.Error("one conversation's digest counted for another")
	}
	// A payload with no conversation id must not silence the digest for
	// everyone: no id, no record.
	rememberDigestInjected(dir, "")
	if digestAlreadyInjected(dir, "") {
		t.Error("an empty conversation id was recorded")
	}
}

// The whole point of reading the transcript: the second call of a turn answers
// the question rather than repeating the digest.
func TestTheSecondCallAnswersTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-old",
		"why does the frobnicator stall on startup",
		"the frobnicator stalled on startup because ZORBWAX_LIMIT was unset; exporting ZORBWAX_LIMIT=4 cleared it")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "transcript.jsonl")
	line := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy does the frobnicator stall on startup\n</USER_REQUEST>\n"}`
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"invocationNum":  1,
		"conversationId": "conv-1",
		"transcriptPath": transcript,
		"workspacePaths": []string{filepath.Join(os.TempDir(), "app")},
	}
	b := mustMarshal(t, payload)
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(b), &out); err != nil {
		t.Fatal(err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("the hook did not answer with JSON: %q", out.String())
	}
	if len(resp.InjectSteps) == 0 {
		t.Fatalf("the question went unanswered on the call after the digest: %q", out.String())
	}
	block := resp.InjectSteps[0].EphemeralMessage
	if !strings.Contains(block, "frobnicator") {
		t.Errorf("the block does not carry the answer: %q", block)
	}
	// And it is the question's own answer rather than the project digest
	// again — the digest carries this session too, so asserting on the text
	// alone would pass with the once-per-conversation behaviour this replaces.
	if strings.Contains(block, antigravityLead) {
		t.Errorf("the call after the digest was handed the digest again: %q", block)
	}
}

// The hook decoded stdin with no time bound, so a host that holds the pipe made
// every turn wait for it to close (#846). Bounding it alone would be worse than
// the wait: a payload deja could not read leaves invocationNum at 0, which the
// turn check reads as the first turn — the digest would go in before every
// model call rather than once.
func TestHookAntigravityBoundsStdinWithoutInjectingOnEveryTurn(t *testing.T) {
	hermeticEnv(t)
	// A store with something in it: with an empty one nothing is injected
	// whatever the payload says, and the check below would pass for the wrong
	// reason.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the anemometer mast bracket weld cracked"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"h1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	if d, _, _, _, _, _, _ := cachedHookDigest(dir); d == "" {
		t.Fatal("fixture has no digest to inject")
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		var out bytes.Buffer
		_ = runHookAntigravity(dir, heldOpenReader{}, &out)
		done <- time.Since(start)
	}()
	select {
	case took := <-done:
		if took > 3*time.Second {
			t.Errorf("the hook waited %s on a held-open pipe", took)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the hook is still waiting for the host to close stdin")
	}

	for _, tc := range []struct {
		name    string
		payload string
		inject  bool
	}{
		{"unreadable payload", "not json at all", false},
		{"empty payload", "", false},
		{"seventh call", `{"invocationNum":7}`, false},
		// Zero is the first call, not one: the harness counts from there and
		// restarts every turn (measured on antigravity-cli 1.1.13).
		{"first call", `{"invocationNum":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runHookAntigravity(dir, strings.NewReader(tc.payload), &out); err != nil {
				t.Fatal(err)
			}
			injected := strings.Contains(out.String(), "injectSteps")
			if injected != tc.inject {
				t.Errorf("%s: injected=%v, want %v — %s", tc.name, injected, tc.inject, out.String())
			}
		})
	}
}

// heldOpenReader never returns data and never reports EOF, like a host that
// keeps the pipe open for the life of the turn.
type heldOpenReader struct{}

func (heldOpenReader) Read([]byte) (int, error) {
	select {}
}

var _ io.Reader = heldOpenReader{}

// An ordinary `agy -p` inside a project sends no workspace at all, and the hook
// runs with its working directory set to the plugin folder — so without another
// source the digest is scoped to a directory nobody works in. Antigravity keeps
// the answer beside the transcript: a cache of which conversation each
// directory last opened.
func TestTheWorkspaceIsFoundWhenThePayloadOmitsIt(t *testing.T) {
	root := t.TempDir()
	product := filepath.Join(root, "antigravity-cli")
	transcript := filepath.Join(product, "brain", "conv-7", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(product, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	byDir := map[string]string{
		"/Users/probe/app":       "conv-7",
		"/Users/probe/app/inner": "conv-7",
		"/Users/probe/other":     "conv-9",
	}
	b, err := json.Marshal(byDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "last_conversations.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// The deepest directory that opened this conversation, so a session
	// started in a subdirectory is not filed under its parent. Asked twenty
	// times, because a map is walked in no particular order and "whichever
	// came first" would pass a single call about half the time.
	for i := range 20 {
		if got := workspaceFromConversation(transcript, "conv-7"); got != "/Users/probe/app/inner" {
			t.Fatalf("workspace = %q on attempt %d", got, i+1)
		}
	}
	// Another conversation is another directory.
	if got := workspaceFromConversation(transcript, "conv-9"); got != "/Users/probe/other" {
		t.Errorf("workspace for the other conversation = %q", got)
	}
	// And what it cannot answer it leaves empty rather than guessing.
	if got := workspaceFromConversation(transcript, "conv-none"); got != "" {
		t.Errorf("an unknown conversation resolved to %q", got)
	}
	if got := workspaceFromConversation("", "conv-7"); got != "" {
		t.Errorf("no transcript path resolved to %q", got)
	}
	if got := workspaceFromConversation(filepath.Join(root, "nope", "brain", "c", "a", "b", "t.jsonl"), "conv-7"); got != "" {
		t.Errorf("a missing cache resolved to %q", got)
	}
}

// And the hook uses it: a payload with no workspace must still recall the
// project the conversation belongs to, not the plugin directory it happens to
// run in.
func TestTheHookRecallsTheProjectWhenThePayloadOmitsIt(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "wsapp", "sess-ws",
		"why does the widget loader stall", "it stalled until zorbwaxneedle was set")
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	product := filepath.Join(tmp, "antigravity-cli")
	transcript := filepath.Join(product, "brain", "conv-ws", ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(product, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// seedClaude files its sessions under /tmp/<project>, which is the path
	// the conversation cache has to name for the two to meet.
	project := "/tmp/wsapp"
	memDir := filepath.Join(claude, "-tmp-wsapp", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "feedback_rule.md"), []byte("---\ndescription: Always pass --race to wsapp tests (auto-mem-needle)\ntype: feedback\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{project: "conv-ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cache, "last_conversations.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"invocationNum":  0,
		"conversationId": "conv-ws",
		"transcriptPath": transcript,
		"workspacePaths": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "zorbwaxneedle") {
		t.Errorf("the conversation's own project was not recalled:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "auto-mem-needle") {
		t.Errorf("Claude auto-memory was not attached to hook-antigravity:\n%s", out.String())
	}
}

func TestAntigravityProjectWorkspaceNarrowsHomeToActiveCheckout(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	repo := filepath.Join(home, "coding", "goprojects", "deja-vu")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repo, "cmd", "deja")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(srcDir, "mcp.go")
	if err := os.WriteFile(targetFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := []transcriptStep{
		{Type: "USER_INPUT"},
		{
			Type: "PLANNER_RESPONSE",
			ToolCalls: []any{
				map[string]any{"name": "noop"},
				map[string]any{
					"name": "mcp_fs_read",
					"args": map[string]any{
						"Path":  `"relative/path.go"`,
						"paths": []any{filepath.Join(home, ".gemini", "brain", "scratch.md"), "/outside/workspace/file.go", targetFile},
					},
				},
			},
		},
	}
	if got := antigravityProjectWorkspace(home, steps); got != repo {
		t.Fatalf("antigravityProjectWorkspace(%q) = %q, want %q", home, got, repo)
	}
	if got := antigravityProjectWorkspace(home, nil); got != home {
		t.Fatalf("antigravityProjectWorkspace(%q, nil) = %q, want %q", home, got, home)
	}
	// A non-home workspace is kept as-is.
	other := filepath.Join(home, "coding", "other-app")
	if got := antigravityProjectWorkspace(other, steps); got != other {
		t.Fatalf("antigravityProjectWorkspace(%q) = %q, want %q", other, got, other)
	}
}

func TestAntigravityMultiWorkspaceSelectsActiveRepo(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	missing := filepath.Join(tmp, "missing-user")
	repo := filepath.Join(tmp, "coding", "active-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(tmp, "coding", "other-repo")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "active-repo", "s-active.jsonl"), "s-active", []string{
		`{"type":"user","sessionId":"s-active","cwd":"` + filepath.ToSlash(repo) + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"token refresh"}}`,
		`{"type":"assistant","sessionId":"s-active","cwd":"` + filepath.ToSlash(repo) + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"We settled on rotating refresh tokens atomically in active-repo."}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "other-repo", "s-other.jsonl"), "s-other", []string{
		`{"type":"user","sessionId":"s-other","cwd":"` + filepath.ToSlash(other) + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"other work"}}`,
		`{"type":"assistant","sessionId":"s-other","cwd":"` + filepath.ToSlash(other) + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"other repo token refresh note."}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	logs := filepath.Join(tmp, "brain", "agy-multi", ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(logs, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-07T10:00:00Z","content":"<USER_REQUEST>how should token refresh work?</USER_REQUEST>"}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := mustMarshal(t, map[string]any{
		"invocationNum":  0,
		"workspacePaths": []string{missing, repo},
		"transcriptPath": transcript,
		"conversationId": "agy-multi",
	})
	var out bytes.Buffer
	if err := runHookAntigravity(dir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rotating refresh tokens atomically in active-repo") {
		t.Fatalf("expected active-repo recall when missing path came first, got %q", out.String())
	}
	if strings.Contains(out.String(), "other repo token refresh note") {
		t.Fatalf("leaked other repo recall: %q", out.String())
	}
}

// A hook fed something that is not a payload carries on with the zero value,
// and what that costs depends on what else names the project. On a host that
// exports nothing and runs the hook outside the project — the shape #759 is
// about — the call stands nowhere: no project, no memory, no row. On a host
// that does export it, the memory still goes out and the log records an
// injection whose receiver is unknown, which is the half #2161 asks about.
// Both are here, because a fix for either has to keep the other in view.
//
// The first version of this test ran the broken payloads after a good one in
// the same process, where `adoptHookCWD`'s export was still standing from the
// good call — so it measured a leak rather than the product and concluded that
// the memory still goes out. Each case starts here where a fresh process
// starts: nothing adopted.
func TestAPayloadDejaCannotReadLosesTheProjectItNamed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "startterm", []string{
		`{"type":"user","sessionId":"startterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Neither the environment nor the working directory names the project, so
	// what the hook knows is what the payload told it — the case #759 is about,
	// and the only shape where the payload's own contribution is visible.
	elsewhere := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := index.DefaultDir()

	// The premise: a payload deja can read injects the memory and records where
	// it went.
	withHookStdin(t, hookPayload(t, map[string]string{"source": "startup", "session_id": "ses1", "cwd": cwd}))
	out := captureStdout(t, func() { runHookContextPlain(t) })
	if !strings.Contains(out, "transaction mode") {
		t.Fatalf("the hook injected nothing on a good payload, so this measures nothing:\n%q", out)
	}
	if into := latestSnapshotInto(t, dir); into != "ses1" {
		t.Fatalf("a good payload recorded the receiver as %q", into)
	}

	for _, tc := range []struct{ name, payload string }{
		{"prose", "this is not json at all"},
		{"truncated", `{"session_id":`},
		{"binary", "\x00\x01binary"},
		{"empty", ""},
	} {
		// Where a fresh process starts: the export the good call above left is
		// not something another invocation would have.
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		before := len(usage.Snapshots(dir, 0))
		withHookStdin(t, tc.payload)
		said := captureStdout(t, func() { runHookContextPlain(t) })
		if strings.Contains(said, "transaction mode") {
			t.Errorf("%s: a payload deja could not read was answered with a project's memory anyway:\n%q", tc.name, said)
		}
		if strings.TrimSpace(said) != "" {
			t.Errorf("%s: the hook said something on a payload it could not read:\n%q", tc.name, said)
		}
		// And nothing is recorded, so the log does not carry an injection that
		// never happened — which is the other half of what #2161 asks: an
		// unreadable payload leaves no trace at all, of the call or of the
		// silence.
		if after := len(usage.Snapshots(dir, 0)); after != before {
			t.Errorf("%s: the injection log grew by %d rows for a call that injected nothing", tc.name, after-before)
		}
	}

	// The other host: it exports the project, so a payload deja cannot read
	// still knows where it is standing. The memory goes out and the log keeps
	// a row — with no receiver, because that is what the payload was carrying.
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	before := len(usage.Snapshots(dir, 0))
	withHookStdin(t, "this is not json at all")
	out = captureStdout(t, func() { runHookContextPlain(t) })
	if !strings.Contains(out, "transaction mode") {
		t.Errorf("a host that names the project got no memory from a broken payload:\n%q", out)
	}
	if after := len(usage.Snapshots(dir, 0)); after != before+1 {
		t.Fatalf("the injection log grew by %d rows, so the receiver below is another call's", after-before)
	}
	// #2161 asks whether this should stay empty; when that is decided, this is
	// the line to change.
	if into := latestSnapshotInto(t, dir); into != "" {
		t.Errorf("the log names %q as the receiver of an injection whose payload deja could not read", into)
	}
}

// latestSnapshotInto is who the newest recorded injection went to.
func latestSnapshotInto(t *testing.T, dir string) string {
	t.Helper()
	snaps := usage.Snapshots(dir, 1)
	if len(snaps) == 0 {
		t.Fatal("nothing was recorded in the injection log")
	}
	return snaps[0].Into
}

// The budget beside hookAdvice is only worth writing if a hook added later has
// to appear in it. Both lists are the same set: every hook a person can type,
// and every hook with a number to be held to (#2668).
func TestEveryHookHasABudgetLine(t *testing.T) {
	src, err := os.ReadFile("hook_typed_by_hand.go")
	if err != nil {
		t.Fatal(err)
	}
	budget, _, ok := strings.Cut(string(src), "// hookAdvice is what to reach for")
	if !ok {
		t.Fatal("the budget comment is gone; it lives above hookAdvice")
	}
	for name := range hookAdvice {
		if !strings.Contains(budget, name) {
			t.Errorf("%s has advice but no budget line", name)
		}
	}
	// And the one hook that is not in hookAdvice, because nobody types it by
	// hand expecting a screen — it still has a number.
	if !strings.Contains(budget, "hook-context") {
		t.Error("hook-context has no budget line")
	}
}

// The dispatcher is the authority on which hooks exist; a new one must reach
// the budget rather than be discovered by a reader wondering what it costs.
func TestEveryDispatchedHookIsInTheBudget(t *testing.T) {
	src, err := os.ReadFile("hook_typed_by_hand.go")
	if err != nil {
		t.Fatal(err)
	}
	budget, _, _ := strings.Cut(string(src), "// hookAdvice is what to reach for")
	for name := range commands {
		if !strings.HasPrefix(name, "hook-") {
			continue
		}
		if !strings.Contains(budget, name) {
			t.Errorf("%s is dispatched but has no budget line", name)
		}
	}
}

// builtFindingWall is recorded with zsh's position prefix, which the index
// strips, so the note shows the error as it was hit in either shell.
const builtFindingWall = "zsh:1: command not found: timeout"

// seedBuiltFindingStore writes claude sessions, and with wall set two claude
// sessions and one codex session that hit the same error, then builds.
func seedBuiltFindingStore(t *testing.T, wall bool, asked string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	claude := filepath.Join(tmp, "claude", "-work-app")
	user := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":` + mustJSON(t, text) + `}}`
	}
	tool := func(id, at, text string) string {
		return `{"type":"user","sessionId":"` + id + `","cwd":"/work/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":` + mustJSON(t, text) + `}]}}`
	}
	for i, at := range []string{"2026-08-02T10:00:00Z", "2026-08-09T10:00:00Z", "2026-08-16T10:00:00Z"} {
		id := "s" + string(rune('a'+i))
		lines := []string{user(id, at, "wire the release script for build "+id)}
		if asked != "" && i < 2 {
			lines = append(lines, user(id, at, asked))
		}
		if wall && i < 2 {
			lines = append(lines, tool(id, at, builtFindingWall))
		}
		writeClaudeFixture(t, filepath.Join(claude, id+".jsonl"), id, lines)
	}
	if wall {
		codex := filepath.Join(tmp, "codex", "sessions", "2026", "08", "20", "rollout-2026-08-20T09-00-00-cx1.jsonl")
		if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
			t.Fatal(err)
		}
		body := strings.Join([]string{
			`{"type":"session_meta","timestamp":"2026-08-20T09:00:00Z","payload":{"id":"cx1","session_id":"cx1","cwd":"/work/app"}}`,
			`{"type":"message","timestamp":"2026-08-20T09:00:01Z","payload":{"role":"user","content":"tag the release"}}`,
			`{"timestamp":"2026-08-20T09:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{\"command\":[\"git\",\"tag\"]}"}}`,
			`{"timestamp":"2026-08-20T09:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":` + mustJSON(t, builtFindingWall) + `}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(codex, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sessionStartNote runs the session-start hook the way Claude Code does and
// returns what it shows the person.
func sessionStartNote(t *testing.T, dir string) string {
	t.Helper()
	cwd := filepath.Join(t.TempDir(), "work", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	payload := mustMarshal(t, map[string]any{"session_id": "first", "cwd": cwd, "source": "startup"})
	withHookStdin(t, string(payload))
	out := captureStdout(t, func() { _ = runHookContext(dir, false) })
	var resp sessionStartHookResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("not the hook envelope: %v\n%s", err, out)
	}
	return resp.SystemMessage
}

// The first session after install said only counts; the one concrete thing
// the history holds was printed by `deja install` alone, which a marketplace
// install never runs (#4616).
func TestTheFirstSessionNamesTheErrorThisMachineKeepsHitting(t *testing.T) {
	dir := seedBuiltFindingStore(t, true, "")
	msg := sessionStartNote(t, dir)
	if !strings.Contains(msg, "deja indexed 4 sessions (2 agents)") {
		t.Fatalf("no built note, so nothing was measured:\n%s", msg)
	}
	want := "this machine has hit `command not found: timeout` in 3 sessions (claude, codex)"
	if !strings.Contains(msg, want) {
		t.Fatalf("the first session does not name the error and its agents:\n%s\nwant a line %q", msg, want)
	}
	checkPersonLines(t, "the built note", builtNoteLines(msg), 2)
}

// builtNoteLines is the built note out of a session start's message.
func builtNoteLines(msg string) string {
	i := strings.Index(msg, "deja indexed")
	if i < 0 {
		return ""
	}
	return msg[i:]
}

// A history with no finding keeps the note it had.
func TestTheBuiltNoteWithoutAFindingIsUnchanged(t *testing.T) {
	dir := seedBuiltFindingStore(t, false, "")
	got := builtNote(dir)
	if want := builtLine(3, 1, 0, true); got != want {
		t.Fatalf("built note = %q, want %q", got, want)
	}
}

// With no error to name, the note quotes a question asked again a week later,
// but only to the host's own UI. `hook-context --notes` feeds plugin replies,
// and OpenClaw's /deja answers in group chats too.
func TestTheBuiltNoteQuotesAQuestionOnlyToTheOwner(t *testing.T) {
	const asked = "why does the release script drop the changelog footer"
	dir := seedBuiltFindingStore(t, false, asked)
	shared := humanNotesFor(dir, false)
	if !strings.HasPrefix(shared, "deja indexed 3 sessions") {
		t.Fatalf("no built note on --notes, so nothing was measured: %q", shared)
	}
	if strings.Contains(shared, "release script drop") || strings.Contains(shared, "\n") {
		t.Fatalf("the notes a plugin may post to a group quote the person's question:\n%s", shared)
	}

	dir = seedBuiltFindingStore(t, false, asked)
	own := humanNotes(dir)
	want := `you asked "` + asked + `" in 2 sessions (claude)`
	if !strings.HasSuffix(own, "\n"+want) {
		t.Fatalf("the owner's note does not quote the repeated question:\n%s\nwant %q", own, want)
	}
	checkPersonLines(t, "the built note", own, 2)
}

// Both finding lines stay in one note line however long the error or the
// question, and a cut error keeps both of its ends.
func TestTheBuiltFindingFitsANoteLine(t *testing.T) {
	long := "Error: connect ECONNREFUSED while pulling registry.internal.example.com/team/platform/service-image:2026.10.14-build.1234 from mirror: connection refused"
	agents := []string{"claude", "codex", "cursor", "gemini", "opencode", "copilot"}
	for name, line := range map[string]string{
		"error":     builtErrorLine(long, 123456, agents),
		"cjk error": builtErrorLine(strings.Repeat("错误：无法连接到数据库服务器", 8), 12, agents[:2]),
		"question":  builtAskedLine(strings.Repeat("why does the nightly exporter drop rows ", 5), 4, agents),
		"short":     builtErrorLine(builtFindingWall, 3, agents[:2]),
	} {
		checkPersonLines(t, name, line, 1)
		if !strings.Contains(line, "sessions (") {
			t.Errorf("%s lost its count and agents: %q", name, line)
		}
	}
	if got := builtErrorLine(builtFindingWall, 3, nil); strings.Contains(got, "()") || !strings.HasSuffix(got, " in 3 sessions") {
		t.Errorf("sessions with no agent named left empty parentheses: %q", got)
	}
	got := builtErrorLine(long, 3, agents[:2])
	if !strings.Contains(got, "Error: connect") || !strings.Contains(got, "connection refused`") || !strings.Contains(got, "…") {
		t.Errorf("the cut error lost an end: %q", got)
	}
	if w := termwidth.Columns(got); w < noteWidth-2 {
		t.Errorf("the cut error left %d columns unused: %q", noteWidth-w, got)
	}
}

// The first session start that finds a manifest says what the build found,
// once; the next one does not (#3073).
func TestTheFirstSessionAfterTheBuildHearsWhatWasIndexed(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	q := `{"type":"user","message":{"role":"user","content":"why does the retry loop drop the last attempt"},"timestamp":"2026-03-01T10:00:00Z","sessionId":"%s","cwd":"/proj"}`
	for _, id := range []string{"s1", "s2"} {
		line := strings.Replace(q, "%s", id, 1)
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	read := func() string {
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return resp.SystemMessage
	}
	first := read()
	if !strings.Contains(first, "deja indexed 2 sessions (1 agent") {
		t.Fatalf("the first session after the build was not told what was indexed: %q", first)
	}
	if !strings.Contains(first, "deja indexed 2 sessions (1 agent, 1 repeat question) · docs: vshulcz.github.io/deja-vu\n") &&
		!strings.HasSuffix(first, "deja indexed 2 sessions (1 agent, 1 repeat question) · docs: vshulcz.github.io/deja-vu") {
		t.Fatalf("the built note is not the one line with the repeat count and the docs: %q", first)
	}
	// One once-only note per session start: the GitHub line waits for the
	// next one rather than making this one a wall.
	if strings.Contains(first, starText) {
		t.Fatalf("the built note and the GitHub line arrived together: %q", first)
	}
	second := read()
	if strings.Contains(second, "deja indexed") {
		t.Fatalf("the built note repeated: %q", second)
	}
	if !strings.Contains(second, starText) {
		t.Fatalf("a marketplace install never heard the one line deja says about itself: %q", second)
	}
	if third := read(); strings.Contains(third, starText) {
		t.Fatalf("the star line repeated: %q", third)
	}
}

func TestTheBuiltNoteNeverReachesTheModel(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	out, err := captureRun(t, "hook-context", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deja indexed") {
		t.Fatalf("the built note went into the model's context:\n%s", out)
	}
}

// The digest cache lives beside the index directory, not inside it, so wiping
// the index left the hook answering from a snapshot of a store that no longer
// exists — and asking for nothing, so it kept answering from that snapshot
// forever. Caches do get wiped: ~/.cache is fair game for cleanup tools and CI
// images (#874).
func TestHookAsksForARebuildWhenTheIndexIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	// hermeticEnv marks every test in this package as the warmup child, which
	// is exactly the process that must not ask for another warmup.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")

	entry := hookCacheEntry{At: time.Now(), CWD: cwd, Gate: hookGate(), Digest: "pool exhausted under load", Sessions: 1, Raw: 42}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookCachePath(dir, cwd), b, 0o600); err != nil {
		t.Fatal(err)
	}
	// No index at all: the directory the cache was built from is gone.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()

	spawned := 0
	saved := spawnWarmup
	spawnWarmup = func(_, _ string) error { spawned++; return nil }
	t.Cleanup(func() { spawnWarmup = saved })

	digest, sessions, _, _, _, _, _ := cachedHookDigest(dir)
	if !strings.Contains(digest, "pool exhausted") || sessions != 1 {
		t.Errorf("the cached digest was dropped rather than served: %q (%d sessions)", digest, sessions)
	}
	if spawned == 0 {
		t.Error("the hook served a store that is gone and asked for no rebuild")
	}

	// A healthy index asks for nothing: the sentinel exists so a session start
	// does not spawn a build on every hook (#825).
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()
	spawned = 0
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	clearWarmupSentinel()
	cachedHookDigest(dir)
	if spawned != 0 {
		t.Errorf("a healthy index still asked for %d rebuild(s)", spawned)
	}
}

// The injection log records which projects a digest was built from, and the
// cache is the path almost every session takes: a miss happens once per
// project, a hit happens every time after. Dropping the names on the way in
// made every one of those hits read as an injection from no project at all
// (#2889).
func TestTheHookCacheKeepsTheProjectsBehindTheDigest(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "proj")
	writeHookCache(dir, cwd, "a digest", 2, 10, nil, 0, []string{"claude:s1"}, []string{"acme-api"})

	_, _, _, _, _, ids, projects := cachedHookDigestFor(dir, cwd, "")
	if len(ids) != 1 || ids[0] != "claude:s1" {
		t.Fatalf("session ids came back as %v", ids)
	}
	if len(projects) != 1 || projects[0] != "acme-api" {
		t.Fatalf("projects came back as %v, want the name the digest was built from", projects)
	}
}

func TestCachedHookDigestServesWithinTTLAndInvalidates(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + old + `","message":{"role":"user","content":"the original digest content marker_alpha"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	d1, s1, _, _, _, _, _ := cachedHookDigest(dir)
	if d1 == "" || s1 == 0 {
		t.Fatal("first call must build a digest")
	}
	// New work lands, index updated — but within the TTL the hook must serve
	// the cached digest without touching the index.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "two.jsonl"), "two", []string{
		`{"type":"user","sessionId":"two","timestamp":"` + time.Now().Add(-25*time.Hour).UTC().Format(time.RFC3339) + `","message":{"role":"user","content":"a fresher session marker_beta"}}`,
	})
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	d2, _, _, _, _, _, _ := cachedHookDigest(dir)
	if d2 != d1 {
		t.Fatal("within TTL the cached digest must be served verbatim")
	}
	// Expire the cache: startup must still serve the stale digest instantly
	// (stale-while-revalidate) — freshness arrives via the detached refresh.
	cachePath := hookCachePath(dir, cwd)
	var e hookCacheEntry
	b, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	e.At = time.Now().Add(-2 * hookDigestTTL)
	nb := mustMarshal(t, e)
	if err := os.WriteFile(cachePath, nb, 0o600); err != nil {
		t.Fatal(err)
	}
	d3, _, _, _, _, _, _ := cachedHookDigest(dir)
	if d3 != d1 {
		t.Fatalf("expired cache must serve stale instantly, got:\n%s", d3)
	}
	// The refresh itself must produce the fresh view.
	runHookRefresh(dir)
	d3b, _, _, _, _, _, _ := cachedHookDigest(dir)
	if !strings.Contains(d3b, "marker_beta") {
		t.Fatalf("refresh must rebuild with fresh sessions:\n%s", d3b)
	}
	d3 = d3b
	// A different cwd must never reuse another project's cache.
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	d4, _, _, _, _, _, _ := cachedHookDigest(dir)
	if d4 == d3 && d3 != "" {
		t.Fatal("cache must be scoped to cwd")
	}
}

func TestHookContextNeverRebuildsIndex(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// A fresh session so the handoff-tip path (the one that used to index
	// synchronously) actually engages.
	fresh := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-app", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + fresh + `","message":{"role":"user","content":"recent work marker"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	// Dirty the store: a new session file appears after the index was built.
	late := `{"type":"user","sessionId":"late1","timestamp":"` + fresh + `","message":{"role":"user","content":"late arrival"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeRoot, "-tmp-app", "late1.jsonl"), []byte(late), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runHookContext(dir, true); err != nil {
		t.Fatal(err)
	}
	// If the hook indexed, the late session is now findable; startup must
	// never pay for indexing, so it must not be.
	if _, ok, _ := index.FindByPrefix(dir, "late1"); ok {
		t.Fatal("session-start hook indexed a dirty store — startup must never pay for indexing")
	}
}

// Credit said aloud is the only signal that a recall helped, and it was only
// linkable back to its session by title text — two sessions that open with the
// same question are common, so the credit landed on whichever one matched
// lexically. The citation carries the session id so the link is a fact.
func TestOpenerLineCarriesTheSessionID(t *testing.T) {
	s := model.Session{
		ID:      "8f2c19ab77d40e6b5c31",
		Harness: "claude",
		Updated: time.Now().AddDate(0, 0, -2),
		Messages: []model.Message{
			{Role: "user", Text: "why does the reconciler double count refunds"},
		},
	}
	line := openerLine(s, nil)
	if !strings.Contains(line, "deja:"+shortID(s.ID)) {
		t.Errorf("citation cannot be linked back to the session it came from: %q", line)
	}
	// The full id is not printed: the line is read aloud to the user, and a
	// twelve-character prefix already selects one session everywhere else.
	if strings.Contains(line, s.ID) {
		t.Errorf("citation spends the whole id on a line meant to be spoken: %q", line)
	}
}

// The citation line is pre-written for the agent to say aloud, and it dropped
// the year: a decision from July 2025 was narrated as "Jul 3", which reads as
// five weeks ago. Age is the whole question on an old recall — the user has to
// hear that the memory is a year old to judge whether it still holds (#R13).
func TestOpenerLineKeepsTheYearOfAnOldRecall(t *testing.T) {
	old := model.Session{Harness: "claude", Updated: time.Now().AddDate(-1, -1, 0),
		Messages: []model.Message{{Role: "user", Text: "why does the reconciler double count refunds"}}}
	line := openerLine(old, nil)
	year := old.Updated.Local().Format("2006")
	if !strings.Contains(line, year) {
		t.Errorf("a %s recall is narrated without its year: %q", year, line)
	}

	// This year's sessions keep the short form: the year adds nothing there
	// and the line is read aloud.
	recent := old
	recent.Updated = time.Now().AddDate(0, 0, -3)
	line = openerLine(recent, nil)
	if strings.Contains(line, recent.Updated.Local().Format("2006")) {
		t.Errorf("this year's recall carries a redundant year: %q", line)
	}
}

func TestHookContextHandsClaudeAutoMemoryToNonClaudeAndNotClaude(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeRoot := filepath.Join(tmp, "claude", "projects")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })

	work := "/tmp/tuip/payments"
	encoded := strings.ReplaceAll(work, "/", "-")
	memDir := filepath.Join(claudeRoot, encoded, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ruleFile := filepath.Join(memDir, "feedback_retry.md")
	ruleContent := "---\n" +
		"name: retry-backoff\n" +
		"description: Retries in payments use internal/retry.Backoff; a hand-rolled sleep loop is rejected in review (zebra-quartz-17)\n" +
		"type: feedback\n" +
		"---\n" +
		"Always use internal/retry.Backoff in payments.\n"
	if err := os.WriteFile(ruleFile, []byte(ruleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	runHook := func(payload string) sessionStartHookResponse {
		t.Helper()
		withHookStdin(t, payload)
		out := captureStdout(t, func() {
			if err := runHookContext(dir, false); err != nil {
				t.Fatalf("runHookContext: %v", err)
			}
		})
		if strings.TrimSpace(out) == "" {
			return sessionStartHookResponse{}
		}
		var resp sessionStartHookResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
			t.Fatalf("unmarshal response: %v\n%s", err, out)
		}
		return resp
	}

	// 1. Exact #5095 repro: non-Claude session gets the Claude auto-memory rule.
	codexResp := runHook(`{"session_id":"probe-c1","cwd":"/tmp/tuip/payments","hook_event_name":"SessionStart","source":"startup"}`)
	if !strings.Contains(codexResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("non-Claude session did not receive zebra-quartz-17:\n%s", codexResp.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(codexResp.HookSpecificOutput.AdditionalContext, "[feedback_retry.md]") {
		t.Fatalf("block did not name the memory file for user visibility/veto:\n%s", codexResp.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(codexResp.SystemMessage, "Claude auto-memory") {
		t.Fatalf("receipt did not announce Claude auto-memory carryover: %q", codexResp.SystemMessage)
	}

	// 2. Claude Code session in the same directory must not get its own auto-memory twice.
	claudeTranscript := filepath.Join(claudeRoot, encoded, "claude-s1.jsonl")
	claudePayload, err := json.Marshal(map[string]string{
		"session_id":      "claude-s1",
		"cwd":             work,
		"transcript_path": claudeTranscript,
		"hook_event_name": "SessionStart",
		"source":          "startup",
	})
	if err != nil {
		t.Fatal(err)
	}
	claudeResp := runHook(string(claudePayload))
	if strings.Contains(claudeResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("Claude session received its own auto-memory twice:\n%s", claudeResp.HookSpecificOutput.AdditionalContext)
	}

	// 3. User veto via policy.json "ignore" suppresses the memory file.
	polFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", polFile)
	if err := os.WriteFile(polFile, []byte(`{"ignore":["*feedback_retry.md*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	vetoedResp := runHook(`{"session_id":"probe-c2","cwd":"/tmp/tuip/payments","hook_event_name":"SessionStart","source":"startup"}`)
	if strings.Contains(vetoedResp.HookSpecificOutput.AdditionalContext, "zebra-quartz-17") {
		t.Fatalf("policy.json ignore did not veto feedback_retry.md:\n%s", vetoedResp.HookSpecificOutput.AdditionalContext)
	}
}

// Every hook line deja writes is a command a shell parses, and the launcher
// lives under the home directory — so a home with a space in its name made all
// of them unrunnable: `sh: /tmp/deja: No such file or directory`, on every
// prompt, for every harness (#3692).
//
// The invariant, rather than a case per writer: install every auto target into
// a home whose name has a space in it, then take each command line out of the
// files and require its first word to name a file that is there.
func TestEveryHookLineRunsFromAHomeWithASpace(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "a home with spaces")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	spaceyEnv(t, home)
	for _, target := range installTargetNames() {
		if !strings.HasSuffix(target, "-auto") {
			continue
		}
		if _, err := captureRun(t, "install", target, "--no-index"); err != nil {
			continue // a target that refuses here has written nothing
		}
	}
	lines := 0
	_ = filepath.Walk(home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // the test walks its own temp tree
		if rerr != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		for _, cmd := range hookCommandLinesIn(string(b)) {
			lines++
			first := firstShellWord(cmd)
			if _, serr := os.Stat(first); serr != nil {
				t.Errorf("%s runs a command whose first word is not a file:\n  %s\n  first word: %s",
					strings.TrimPrefix(p, home), cmd, first)
			}
		}
		return nil
	})
	if lines == 0 {
		t.Fatal("no hook command lines found — the walk is not reading what the writers wrote")
	}
	t.Logf("checked %d hook command lines", lines)
}

// spaceyEnv is hermeticEnv with a home of the test's choosing, so the paths
// the writers build carry the space.
func spaceyEnv(t *testing.T, home string) {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DEJA_GOOSE_ROOT", filepath.Join(home, ".local", "share", "goose"))
}

// hookCommandLinesIn pulls the command lines out of a config: any quoted
// string that runs one of deja's subcommands. Every format here quotes its
// values — JSON, JSONC, TOML, YAML — so one reader answers for all of them.
func hookCommandLinesIn(text string) []string {
	var out []string
	for _, quote := range []byte{'"', '\''} {
		for _, span := range quotedSpans(text, quote) {
			cmd := quotedPathUnescape.Replace(span)
			if !hasDejaSubcommand(cmd) {
				continue
			}
			out = append(out, cmd)
		}
	}
	return out
}

// quotedSpans are the contents of every quoted string in the text, taking an
// escaped quote as part of the string rather than its end.
func quotedSpans(text string, quote byte) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != quote {
			continue
		}
		for j := i + 1; j < len(text); j++ {
			if text[j] == '\n' {
				i = j
				break
			}
			if text[j] != quote {
				continue
			}
			if j > 0 && text[j-1] == '\\' {
				continue
			}
			out = append(out, text[i+1:j])
			i = j
			break
		}
	}
	return out
}

// hasDejaSubcommand reports whether a command line runs one of deja's own
// subcommands, which is what makes it a line deja wrote rather than a path
// mentioned in prose.
func hasDejaSubcommand(cmd string) bool {
	for field := range strings.FieldsSeq(cmd) {
		if dejaSubcommandInConfigs[strings.Trim(field, `"'`)] {
			return strings.Contains(cmd, "deja")
		}
	}
	return false
}

// firstShellWord is the command a shell would run: the quoted span when the
// line starts with a quote, and the first whitespace-delimited word otherwise.
// PowerShell's call operator comes before the path it runs: `& 'C:/…' hook`.
func firstShellWord(cmd string) string {
	cmd = strings.TrimSpace(unwrapPowerShellHook(strings.TrimSpace(cmd)))
	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "& "))
	if cmd == "" {
		return ""
	}
	if q := cmd[0]; q == '\'' || q == '"' {
		if end := strings.IndexByte(cmd[1:], q); end >= 0 {
			return cmd[1 : 1+end]
		}
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// A path that needs no quoting is left alone: a config full of quoted paths
// where none of them needs it is a diff in somebody's dotfiles for nothing.
func TestHookLinesAreNotQuotedWhenTheyNeedNotBe(t *testing.T) {
	if got := hookRun("/usr/local/bin/deja", "hook-prompt"); got != "/usr/local/bin/deja hook-prompt" {
		t.Errorf("an ordinary path was quoted: %q", got)
	}
	got := hookRun("/Users/A B/bin/deja", "hook-prompt", "--strict")
	if !strings.HasSuffix(got, " hook-prompt --strict") || !strings.ContainsAny(got[:1], `"'`) {
		t.Errorf("a path with a space was not quoted: %q", got)
	}
}

// And an entry already written in the unquoted form is deja's own to rewrite,
// not somebody else's line wrapping deja's hook — which is what left a broken
// machine broken through every later install.
func TestTheUnquotedFormIsRecognisedAsOurs(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "a home with spaces", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "deja-hook")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := exe + " hook-prompt"
	if kind := hookCommandKindOf(stale, hookRun(exe, "hook-prompt")); kind != hookDejas {
		t.Errorf("the unquoted form read as %v, not deja's own", kind)
	}
	// A wrapper somebody wrote around deja's hook is still theirs, space in
	// the path or not.
	wrapper := "env FOO=1 " + exe + " hook-prompt"
	if kind := hookCommandKindOf(wrapper, hookRun(exe, "hook-prompt")); kind != hookWrapsDejas {
		t.Errorf("a wrapper read as %v, not as a wrapper", kind)
	}
}

// Claude Code runs hook commands through bash, Git Bash on Windows, and bash
// eats an unquoted backslash: `H:\pycode\Self\deja-vu\deja.exe` reached it as
// `H:pycodeSelfdeja-vudeja.exe`, every hook exited 127 and nothing said so
// (#4116). What deja writes on Windows has to reach bash as the same file.
func TestAWindowsHookPathSurvivesBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for _, path := range []string{
		`H:\pycode\Self\deja-vu\deja.exe`,
		`C:\Program Files\deja\deja.exe`,
		`C:\Users\a b\scoop\shims\deja.exe`,
	} {
		cmd := hookCommandQuoteFor("windows", path)
		out, err := exec.CommandContext(t.Context(), "bash", "-c", "printf %s "+cmd).Output()
		if err != nil {
			t.Fatalf("bash -c %q: %v", cmd, err)
		}
		if want := strings.ReplaceAll(path, `\`, "/"); string(out) != want {
			t.Errorf("wrote %q, bash ran %q, want %q", cmd, out, want)
		}
	}
}

// The same written command, run for real by the other two shells a Windows
// harness may hand it to. The test binary is the executable: asked to run no
// tests, it exits 0, which is all a hook host sees.
func TestAWindowsHookPathRunsInCmdAndPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows shells")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := hookCommandQuote(exe)
	if strings.Contains(cmd, `"`) {
		// PowerShell reads a leading quoted string as a value, not a command;
		// that is true of the quoting before this change as well.
		t.Skipf("the temp path has a space: %s", exe)
	}
	for _, sh := range [][]string{
		{"cmd", "/c", cmd + " -test.run=NoSuchTest"},
		// PowerShell splits an unquoted -test.run=… at the dot; the argument
		// is the test's, the path in front of it is what is under test.
		{"powershell", "-NoProfile", "-Command", cmd + " '-test.run=NoSuchTest'"},
	} {
		if out, err := exec.CommandContext(t.Context(), sh[0], sh[1:]...).CombinedOutput(); err != nil {
			t.Errorf("%s ran %q: %v\n%s", sh[0], sh[len(sh)-1], err, out)
		}
	}
}

// Command Code's shell hooks have no compaction event. Compacting appends a
// compaction record to the transcript and keeps every turn above it, so the
// next PreToolUse, whose payload names the transcript, captures the session as
// it stood and answers with the packet, once.
func TestCommandCodeCompactionIsCaughtUpAtTheNextToolCall(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	root := filepath.Join(t.TempDir(), "cc", "projects")
	t.Setenv("DEJA_COMMANDCODE_ROOT", root)
	transcript := filepath.Join(root, "-work", "cc-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := mustMarshal(t, workspace)
	turns := []string{
		`{"type":"session","version":3,"id":"cc-1","timestamp":"2026-10-07T10:00:00Z","cwd":` + string(cwd) + `}`,
		`{"type":"message","id":"a1","timestamp":"2026-10-07T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}]}}`,
		`{"type":"message","id":"a2","timestamp":"2026-10-07T10:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"shell_command","input":{"command":"go test ./parser/..."}}]}}`,
		`{"type":"message","id":"a3","timestamp":"2026-10-07T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Exit code: 1\n--- FAIL: TestParseSeed"}]}}`,
	}
	write := func(extra ...string) {
		if err := os.WriteFile(transcript, []byte(strings.Join(append(append([]string{}, turns...), extra...), "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tp := mustMarshal(t, transcript)
	tool := `{"session_id":"cc-1","transcript_path":` + string(tp) + `,"cwd":` + string(cwd) +
		`,"hook_event_name":"PreToolUse","tool_name":"shell_command","tool_display_name":"SHELL","tool_input":{"command":"go test ./parser/..."}}`
	write()
	if out := runDeferredHook(t, dir, "hook-tool", nil, tool); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session that never compacted got a packet: %s", out)
	}
	write(
		`{"type":"compaction","id":"c1","parentId":"a3","timestamp":"2026-10-07T10:05:00Z","summary":"summary of the work so far","firstKeptEntryId":"a3"}`,
		`{"type":"message","id":"a4","timestamp":"2026-10-07T10:05:01Z","message":{"role":"assistant","content":[{"type":"text","text":"carrying on"}]}}`,
	)
	out := runDeferredHook(t, dir, "hook-tool", nil, tool)
	for _, want := range []string{"Compaction context", "fix the parser test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "carrying on") {
		t.Errorf("the packet was built from turns after the compaction: %s", out)
	}
	if again := runDeferredHook(t, dir, "hook-tool", nil, tool); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
}

// Amp has no compaction event; its plugin finds one on agent.start and hands
// the turns before the cut to hook-precompact as a thread file, marked as a
// catch-up. Handed over again on the next agent.start, the same compaction is
// not captured or delivered twice.
func TestAmpCatchUpIsCapturedOnce(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	thread := filepath.Join(t.TempDir(), "deja-amp-1.json")
	// Amp writes file:///C:/work on Windows and file:///work elsewhere.
	uri := "file://" + filepath.ToSlash(workspace)
	if !strings.HasPrefix(filepath.ToSlash(workspace), "/") {
		uri = "file:///" + filepath.ToSlash(workspace)
	}
	body := mustMarshal(t, map[string]any{
		"id": "T-1", "title": "", "created": 1791000000000,
		"env": map[string]any{"initial": map[string]any{"trees": []any{map[string]any{"uri": uri}}}},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "fix the tokenizer test"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "the tokenizer fails on escapes"}}},
		},
	})
	if err := os.WriteFile(thread, body, 0o644); err != nil {
		t.Fatal(err)
	}
	payload := mustMarshal(t, map[string]any{
		"session_id": "T-1", "transcript_path": thread, "cwd": workspace, "harness": "amp", "deja_catch_up": true,
	})
	prompt := mustMarshal(t, map[string]any{"prompt": "carry on", "session_id": "T-1", "cwd": workspace})
	ask := func() string {
		var out bytes.Buffer
		if err := runHookPromptMode(dir, bytes.NewReader(prompt), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	withHookStdin(t, string(payload))
	runHookPrecompactFor(dir, "")
	out := ask()
	for _, want := range []string{"Compaction context", "fix the tokenizer test"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	withHookStdin(t, string(payload))
	runHookPrecompactFor(dir, "")
	if again := ask(); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
}

// claudeLines is a Claude Code transcript fragment: "u" a user turn, "a"
// assistant text, "s" the summary Claude files after a compaction, "t" a Bash
// command.
func claudeLines(t *testing.T, workspace string, minute int, pairs ...string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		kind, text := pairs[i], pairs[i+1] //nolint:gosec // pairs come in twos
		rec := map[string]any{
			"sessionId": "compaction-fixture", "cwd": workspace,
			"timestamp": fmt.Sprintf("2026-09-10T11:%02d:%02dZ", minute, i/2),
		}
		switch kind {
		case "u", "s":
			rec["type"] = "user"
			rec["message"] = map[string]any{"role": "user", "content": text}
			if kind == "s" {
				rec["isCompactSummary"] = true
			}
		case "a":
			rec["type"] = "assistant"
			rec["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}
		case "t":
			rec["type"] = "assistant"
			rec["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": fmt.Sprintf("cmd-%d-%d", minute, i), "name": "Bash", "input": map[string]any{"command": text},
			}}}
		}
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCompactionCarriesTheListForward(t *testing.T) {
	dir, workspace, transcript := prepareCompaction(t)
	input := precompactHookInput{SessionID: "compaction-fixture", CWD: workspace, TranscriptPath: transcript}
	carry := func() []model.ContextCarry {
		t.Helper()
		state, found, err := index.Compaction(dir, "compaction-fixture", workspace)
		if err != nil || !found {
			t.Fatalf("no state: found=%v err=%v", found, err)
		}
		return state.Data.Carry
	}

	appendTranscript(t, transcript, claudeLines(t, workspace, 1, "u", "проверь очередь", "a", "PR #420 ждёт мейнтейнера."))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("first capture stored nothing")
	}
	if got := carry(); len(got) != 1 || got[0].Key != "#420" || got[0].Age != 0 {
		t.Fatalf("first compaction: %+v", got)
	}

	// The next segment starts after Claude's summary and never names #420:
	// the line is carried, one compaction older, next to what is new.
	appendTranscript(t, transcript, claudeLines(t, workspace, 2,
		"s", "This session is being continued from a previous conversation that ran out of context.",
		"u", "дальше", "a", "Гипотеза H12 отвергнута замером."))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("second capture stored nothing")
	}
	got := carry()
	if len(got) != 2 || got[0].Kind != "open" || got[0].Age != 1 || got[1].Kind != "verdict" || got[1].Age != 0 {
		t.Fatalf("second compaction did not merge the carried list: %+v", got)
	}
	out := digest.RenderCompactionContext(model.CompactionContext{Carry: got}, 4096)
	if !strings.Contains(out, "Keep until closed") || !strings.Contains(out, "- [open] PR #420") || !strings.Contains(out, "- [verdict] Гипотеза H12") {
		t.Fatalf("the carried list renders as %q", out)
	}

	// A merge command in the next segment closes it.
	appendTranscript(t, transcript, claudeLines(t, workspace, 3,
		"s", "This session is being continued from a previous conversation that ran out of context.",
		"u", "мержи", "t", "gh pr merge 420 --squash"))
	if _, ok := captureCompaction(dir, input); !ok {
		t.Fatal("third capture stored nothing")
	}
	if got := carry(); len(got) != 1 || got[0].Kind != "verdict" || got[0].Age != 1 {
		t.Fatalf("merged PR stayed on the list: %+v", got)
	}
}

// Cline CLI tells its plugin a compaction is starting through onEvent and does
// not wait for the answer, but messages.json keeps every turn through it, the
// summary going to <id>.compaction.json (CLI 3.0.69, measured). The session is
// found by its id; the shapes are the ones that stand wrote.
func TestClineCompactionIsReadFromMessagesJSON(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "1791367873575_myzey"
	session := filepath.Join(sources.ClineSessionsDir(), id)
	manifest := mustMarshal(t, map[string]any{
		"version": 1, "session_id": id, "source": "cli", "started_at": "2026-10-07T10:11:13.579Z",
		"status": "completed", "provider": "openai-compatible", "model": "stub-model",
		"cwd": workspace, "workspace_root": workspace, "prompt": "fix the parser test",
		"messages_path": filepath.Join(session, id+".messages.json"),
	})
	compactionWrite(t, filepath.Join(session, id+".json"), string(manifest))
	messages := mustMarshal(t, map[string]any{
		"version": 1, "updated_at": "2026-10-07T10:11:16.423Z", "agent": "lead", "sessionId": id,
		"origin": map[string]any{"source": "cli", "mode": "user", "sessionId": id, "version": "3.0.69"},
		"messages": []any{
			map[string]any{"id": "u1", "role": "user", "ts": 1791367874407, "content": []any{map[string]any{"type": "text", "text": `<user_input mode="act">fix the parser test</user_input>`}}},
			map[string]any{"id": "a1", "role": "assistant", "ts": 1791367874802, "content": []any{map[string]any{"type": "tool_use", "id": "call_1", "name": "run_commands", "input": map[string]any{"commands": []any{"go test ./parser/..."}}}}},
			map[string]any{"id": "r1", "role": "user", "ts": 1791367875300, "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "name": "run_commands", "content": []any{
				map[string]any{"query": "go test ./parser/...", "result": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n", "success": false},
			}}}},
			map[string]any{"id": "a2", "role": "assistant", "ts": 1791367875900, "content": []any{map[string]any{"type": "text", "text": "The parser test fails: want 3, got 4. I decided to fix parse.go next."}}},
		},
	})
	compactionWrite(t, filepath.Join(session, id+".messages.json"), string(messages))
	out := precompactThen(t, dir, workspace, map[string]any{"session_id": id, "cwd": workspace, "harness": "cline"}, "")
	wantCompactionPacket(t, out)
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "cline" {
		t.Errorf("the capture is filed under %q, want cline", state.Harness)
	}
}

// The plugin answers the status notice that opens a compaction, names the
// session and the harness, and asks the prompt hook again so the packet goes
// into the next request of the same run.
func TestClinePluginCapturesOnCompactionStart(t *testing.T) {
	js := clinePluginJS("/bin/deja")
	for _, want := range []string{
		`meta.kind.endsWith("compaction") && meta.phase === "started"`,
		`run(["hook-precompact"], JSON.stringify({ session_id: session, cwd: workspace || process.cwd(), harness: "cline" }))`,
		`if (prompt !== asked || compacted) {`,
		`"hooks"]`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("cline plugin lacks %q", want)
		}
	}
}

// CodeBuddy Code: PreCompact names the transcript, then SessionStart fires
// with source "compact" — and CodeBuddy drops that hook's context, injecting
// SessionStart context only while the input holds a single user message
// (isFirstOrResumeMessage, 2.161.2). The prompt after it does arrive, so the
// packet waits for UserPromptSubmit rather than being marked delivered into
// nothing (#4705).
func TestCodeBuddyCompactionPacketWaitsForThePrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	ws := mustMarshal(t, workspace)
	transcript := filepath.Join(sources.CodeBuddyRoot(), "-work-app", "cb-1.jsonl")
	compactionWrite(t, transcript, strings.ReplaceAll(strings.Join([]string{
		`{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"fix the parser test"}],"sessionId":"cb-1","cwd":WS}`,
		`{"id":"a1","timestamp":1791187201000,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"go test ./parser/...\"}","sessionId":"cb-1","cwd":WS}`,
		`{"id":"r1","timestamp":1791187202000,"type":"function_call_result","name":"Bash","callId":"call_1","status":"completed","output":{"type":"text","text":"Command: go test ./parser/...\nStdout: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nStderr: (empty)\nExit Code: 1\nSignal: (none)"},"sessionId":"cb-1","cwd":WS}`,
		`{"id":"m2","timestamp":1791187203000,"type":"message","role":"assistant","content":[{"type":"output_text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"sessionId":"cb-1","cwd":WS}`,
	}, "\n")+"\n", "WS", string(ws)))

	pre := mustMarshal(t, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "cwd": workspace, "hook_event_name": "PreCompact", "trigger": "manual"})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, err := index.Compaction(dir, "cb-1", workspace); err != nil || !found {
		t.Fatalf("precompact stored nothing: %v", err)
	}

	// CodeBuddy's SessionStart payload carries no cwd; the hook runs in it.
	t.Chdir(workspace)
	start := mustMarshal(t, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "hook_event_name": "SessionStart", "source": "compact"})
	withHookStdin(t, string(start))
	out := captureStdout(t, func() {
		if err := runHookContextMode(dir, false, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("SessionStart(compact) answered into a context CodeBuddy drops: %s", out)
	}

	got := hostsPrompt(t, dir, map[string]any{"session_id": "cb-1", "transcript_path": transcript, "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt packet lacks %q: %s", want, got)
		}
	}
}

// cursorChat writes a cursor-agent 2026.09 chat the way the CLI keeps it: the
// transcript under projects/<folder>/agent-transcripts/<id>/<id>.jsonl, which
// records the calls and none of their results, and the chat store under
// chats/<md5 of the cwd>/<id>/ with its meta.json, which has the results.
func cursorChat(t *testing.T, workspace, id string) string {
	t.Helper()
	chat := filepath.Join(sources.CursorCLIRoot(), "chats", fmt.Sprintf("%x", md5.Sum([]byte(workspace))), id) //nolint:gosec // Cursor names chat dirs by the md5 of the workspace
	meta := mustMarshal(t, map[string]any{"cwd": workspace})
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
	if out, err := exec.CommandContext(t.Context(), "sqlite3", filepath.Join(chat, "store.db"), sql.String()).CombinedOutput(); err != nil {
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

// DeepSeek Harness appends compaction/start to the session log and shadows the
// summarised turns rather than dropping them, so the log still holds them. The
// plugin sends the session's uuid on compaction/start; deja finds the log by it
// and the next recall carries the packet. Records as dsh 0.1.1-rc.2 wrote them
// on a stand, in the raw (unframed) encoding dsh can be configured to write.
func TestDeepSeekCompactionIsReadFromTheSessionLog(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "03698097-e226-4edb-93a7-b2890b9e469c"
	cwd := mustMarshal(t, workspace)
	output := mustMarshal(t, "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n[exit code: 1]")
	lines := []string{
		`{"type":"session","version":0,"id":"session-` + id + `","createdAt":1791368690569,"cwd":` + string(cwd) + `,"delegationDepth":0}`,
		`{"type":"turn/start","seq":4,"time":1791368690578,"data":{"turn":1}}`,
		`{"type":"step/start","seq":6,"time":1791368690756,"data":{"turn":1,"step":1}}`,
		`{"type":"user/message","seq":7,"time":1791368690756,"data":{"content":[{"type":"text","text":"fix the parser test"}],"source":{"kind":"user"},"role":"user","id":"1417e875-4d5f-47af-bf83-7e1483a25dcb"},"surfaceOp":"append"}`,
		`{"type":"user/message","seq":8,"time":1791368690757,"data":{"content":[{"type":"text","text":"Current runtime context. This snapshot supersedes earlier runtime-context snapshots."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt","form":"snapshot"},"role":"user","id":"r1"},"surfaceOp":"append"}`,
		`{"type":"assistant/message","seq":20,"time":1791368690804,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"tool-call","id":"call_1","name":"bash","arguments":"{\"command\": \"go test ./parser/...\", \"description\": \"run the parser tests\"}"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},"id":"a1"}},"surfaceOp":"append"}`,
		`{"type":"tool/call","seq":21,"time":1791368690805,"data":{"turn":1,"step":1,"callId":"call_1","name":"bash","arguments":"{\"command\": \"go test ./parser/...\", \"description\": \"run the parser tests\"}"}}`,
		`{"type":"tool/result","seq":22,"time":1791368690928,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_1"},"content":[{"type":"tool-result","toolCallId":"call_1","content":[{"type":"text","text":` + string(output) + `}],"isError":false}],"role":"user","id":"t1"}},"surfaceOp":"append"}`,
		`{"type":"assistant/message","seq":23,"time":1791368690930,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},"id":"a2"}},"surfaceOp":"append"}`,
		`{"type":"compaction/start","seq":24,"time":1791368690933,"data":{"compactionId":"3b712367-45bd-4f84-847b-fe9a46642758","turn":1}}`,
	}
	compactionWrite(t, filepath.Join(sources.DeepSeekRoot(), "--w-proj--", "session-"+id, "session.jsonl"), strings.Join(lines, "\n")+"\n")
	// What the plugin sends on compaction/start.
	out := precompactThen(t, dir, workspace, map[string]any{"session_id": id, "cwd": workspace, "harness": "deepseek"}, "")
	wantCompactionPacket(t, out)
}

func TestCompactionFreshnessTracksDirtyStagedAndUntrackedChanges(t *testing.T) {
	repo := compactionGitRepo(t)
	tracked := filepath.Join(repo, "tracked.txt")
	compactionWrite(t, tracked, "base\n")
	compactionGit(t, repo, "add", "tracked.txt")
	compactionGit(t, repo, "commit", "-m", "base")

	compactionWrite(t, tracked, "dirty one\n")
	dirtyOne := compactionFreshness(repo)
	compactionWrite(t, tracked, "dirty two\n")
	dirtyTwo := compactionFreshness(repo)
	if dirtyOne.Error != "" || dirtyTwo.Error != "" || dirtyOne.WorktreeState == dirtyTwo.WorktreeState {
		t.Fatalf("dirty edit was not fingerprinted: first=%#v second=%#v", dirtyOne, dirtyTwo)
	}

	compactionGit(t, repo, "add", "tracked.txt")
	staged := compactionFreshness(repo)
	if staged.Error != "" || staged.WorktreeState == dirtyTwo.WorktreeState {
		t.Fatalf("staging was not fingerprinted: dirty=%#v staged=%#v", dirtyTwo, staged)
	}

	untracked := filepath.Join(repo, "nested", "note.txt")
	compactionWrite(t, untracked, "one\n")
	firstUntracked := compactionFreshness(repo)
	compactionWrite(t, untracked, "two\n")
	secondUntracked := compactionFreshness(repo)
	if firstUntracked.Error != "" || secondUntracked.Error != "" || firstUntracked.WorktreeState == secondUntracked.WorktreeState {
		t.Fatalf("untracked edit was not fingerprinted: first=%#v second=%#v", firstUntracked, secondUntracked)
	}
}

func TestCompactionFreshnessMarksLargeUntrackedFilesPartial(t *testing.T) {
	repo := compactionGitRepo(t)
	artifact := filepath.Join(repo, "generated-artifact")
	f, err := os.Create(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxUntrackedFingerprintFileBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	freshness := compactionFreshness(repo)
	if !strings.HasPrefix(freshness.WorktreeState, "partial:") {
		t.Fatalf("large artifact was treated as complete: %#v", freshness)
	}
	if !strings.Contains(freshness.Error, "partial") {
		t.Fatalf("partial fingerprint did not require validation: %#v", freshness)
	}
}

// The budget is a ceiling against a hung git, not a performance claim, and a
// fingerprint that runs out of it is worse than a slow one: it comes back
// `partial:` and tells the agent not to trust the context it was just given.
// Seven git calls share it, so it has to be sized for the platform where a
// process is expensive — the windows runner spent all 750 ms of the old one on
// a repository with one dirty file (#3645).
//
// A wall-clock reading on a shared runner is mostly the runner's load, so one
// sample against a fixed share of the budget flaked (#5551: 2.4 s of 4 s on a
// loaded windows leg). Each round now times seven bare git processes, the
// count the check makes, right before the check itself. Load slows both alike,
// so the middle ratio of five rounds holds where the wall time does not. A
// check costing more than 1.6 times those seven processes fails: about four
// more git calls, or a stall of half what seven processes take.
func TestCompactionFreshnessBudgetIsSizedForTheSlowPlatform(t *testing.T) {
	repo := compactionGitRepo(t)
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "base\n")
	compactionGit(t, repo, "add", "tracked.txt")
	compactionGit(t, repo, "commit", "-m", "base")
	compactionWrite(t, filepath.Join(repo, "tracked.txt"), "dirty\n")

	// Idle or loaded the middle ratio sits at 0.9-1.3 on a mac; four more
	// calls put it at 1.6-1.9 and twice the calls at 2.0-2.4.
	const gitCalls, rounds, batchesAllowed, maxRatio = 7, 5, 3, 1.6
	sevenBare := func() time.Duration {
		start := time.Now()
		for range gitCalls {
			if out, err := gitread.Command(context.Background(), "-C", repo, "rev-parse", "--is-inside-work-tree").Output(); err != nil {
				t.Fatalf("bare git: %v: %s", err, out)
			}
		}
		return time.Since(start)
	}
	sevenBare() // the first git of a run pays for a cold binary
	best := time.Duration(1 << 62)
	// batch returns the sorted ratios of five rounds. A burst of load can still
	// land on most of one batch, so a batch over the line gets two more
	// chances; a check that really does more work is over in every one.
	batch := func() []float64 {
		ratios := make([]float64, 0, rounds)
		var partial []time.Duration
		for range rounds {
			bare := sevenBare()
			start := time.Now()
			freshness := compactionFreshness(repo)
			took := time.Since(start)
			if freshness.Error != "" || strings.HasPrefix(freshness.WorktreeState, "partial:") {
				// One round past the budget is the runner; most of them is
				// the check (#3645).
				partial = append(partial, took.Round(time.Millisecond))
				continue
			}
			best = min(best, took)
			ratios = append(ratios, float64(took)/float64(bare))
		}
		if len(ratios) <= rounds/2 {
			t.Fatalf("a repository with one dirty file did not fingerprint in %v in %d of %d rounds (they took %v)",
				compactionGitBudget, rounds-len(ratios), rounds, partial)
		}
		slices.Sort(ratios)
		return ratios
	}
	var batches [][]float64
	for len(batches) < batchesAllowed {
		ratios := batch()
		batches = append(batches, ratios)
		if ratios[len(ratios)/2] <= maxRatio {
			break
		}
	}
	last := batches[len(batches)-1]
	ratio := last[len(last)/2]
	t.Logf("freshness at best %v of a %v budget; against %d bare git processes run just before it, ratios by batch %.2f",
		best.Round(time.Millisecond), compactionGitBudget, gitCalls, batches)
	// Half the budget spent on the smallest repository there is means the
	// margin is gone for a real one.
	if best > compactionGitBudget/2 {
		t.Errorf("one dirty file cost at best %v of the %v budget, leaving no margin for a real repository",
			best.Round(time.Millisecond), compactionGitBudget)
	}
	if ratio > maxRatio {
		t.Errorf("one dirty file cost %.2f times what %d bare git processes cost just before it in each of %d batches (%.2f); the check makes %d calls and may cost %.1f times",
			ratio, gitCalls, len(batches), batches, gitCalls, maxRatio)
	}
	if win, unix := compactionBudgetFor("windows"), compactionBudgetFor("linux"); win <= unix {
		t.Errorf("windows budget = %v against %v elsewhere: the platform where a process is expensive needs more, not the same",
			win, unix)
	}
	if compactionBudgetFor(runtime.GOOS) != compactionGitBudget {
		t.Errorf("the budget in use (%v) is not the one this platform gets (%v)",
			compactionGitBudget, compactionBudgetFor(runtime.GOOS))
	}
}

func TestCompactionFreshnessNamesRepositoryErrors(t *testing.T) {
	freshness := compactionFreshness(filepath.Join(t.TempDir(), "not-a-repository"))
	if freshness.Error == "" || freshness.WorktreeState != "" {
		t.Fatalf("non-repository freshness implied usable state: %#v", freshness)
	}
}

func TestFingerprintStreamsFailClosedAfterCancellation(t *testing.T) {
	repo := compactionGitRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := sha256.New()
	if hashGitStream(ctx, h, repo, "status", "--porcelain=v1") {
		t.Fatal("cancelled git stream was accepted")
	}
	if !hashUntrackedStream(ctx, h, repo) {
		t.Fatal("cancelled untracked stream was treated as complete")
	}
}

func compactionGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	compactionGit(t, repo, "init")
	compactionGit(t, repo, "config", "user.email", "fixture@example.invalid")
	compactionGit(t, repo, "config", "user.name", "fixture")
	return repo
}

func compactionGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func compactionWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A git that leaves a child holding stdout (a shim whose real git outlives
// it, #5620) kept the hook reading long after the deadline: killing the direct
// child does not close the pipe, and WaitDelay only closes pipes exec copies
// itself. Both streams have to give up when the deadline ends. The deadline is
// cut only once the orphan exists, and the orphan lives 20s, so the 10s ceiling
// fails on a hang and on nothing else.
func TestFingerprintStreamsStopAtTheDeadlineWhenAnOrphanHoldsStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git is a shell script")
	}
	bin := t.TempDir()
	pids := filepath.Join(bin, "pids")
	script := "#!/bin/sh\nsleep 20 &\necho $! >> '" + pids + "'\nexec sleep 20\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	orphans := func() []string {
		data, _ := os.ReadFile(pids)
		return strings.Fields(string(data))
	}
	t.Cleanup(func() {
		for _, field := range orphans() {
			if pid, err := strconv.Atoi(field); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})

	root := t.TempDir()
	for _, stream := range []struct {
		name    string
		partial func(context.Context) bool
	}{
		{"tracked", func(ctx context.Context) bool { return !hashGitStream(ctx, sha256.New(), root, "diff") }},
		{"untracked", func(ctx context.Context) bool { return hashUntrackedStream(ctx, sha256.New(), root) }},
	} {
		ctx, cancel := context.WithCancel(t.Context())
		before := len(orphans())
		go func() {
			defer cancel()
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if len(orphans()) > before {
					return
				}
			}
		}()
		start := time.Now()
		partial := stream.partial(ctx)
		took := time.Since(start)
		cancel()
		if len(orphans()) == before {
			t.Fatalf("%s: fake git never left an orphan, the case was not exercised", stream.name)
		}
		if !partial {
			t.Errorf("%s: a stream cut off at the deadline was treated as complete", stream.name)
		}
		if took > 10*time.Second {
			t.Errorf("%s: stream returned %v after its deadline, held open by an orphan", stream.name, took.Round(time.Millisecond))
		}
	}
}

// grokUpdates writes a Grok Build 1.0.41 session the way the CLI keeps it:
// summary.json beside the ACP updates.jsonl, under the URL-encoded working
// directory. The lines are the ones a headless run wrote on a stand: the shell
// call arrives as tool_call, then a tool_call_update carrying the title and the
// description, then one carrying the output — three records for one command.
func grokUpdates(t *testing.T, workspace, id string) string {
	t.Helper()
	// Grok names the group after the escaped cwd, which on Windows keeps the
	// drive's colon and is no directory name; the .cwd file beside it is the
	// other place it records the directory, and the one read first.
	group := url.PathEscape(workspace)
	if runtime.GOOS == "windows" {
		group = "ws"
	}
	compactionWrite(t, filepath.Join(sources.GrokRoot(), "sessions", group, ".cwd"), workspace)
	dir := filepath.Join(sources.GrokRoot(), "sessions", group, id)
	summary := mustMarshal(t, map[string]any{
		"info":              map[string]any{"id": id, "cwd": workspace},
		"session_summary":   "fix the parser test",
		"created_at":        "2026-10-07T10:20:23.521115Z",
		"updated_at":        "2026-10-07T10:20:28.119804Z",
		"session_kind":      "headless",
		"generated_title":   "fix the parser test",
		"current_model_id":  "grok-build",
		"last_turn_summary": "The parser test fails: want 3, got 4.",
	})
	compactionWrite(t, filepath.Join(dir, "summary.json"), string(summary))
	rec := func(ts int64, update string) string {
		return `{"timestamp":` + jsonInt(ts) + `,"method":"session/update","params":{"sessionId":"` + id + `","update":` + update + `,"_meta":{"agentTimestampMs":` + jsonInt(ts*1000) + `}}}`
	}
	lines := []string{
		rec(1791368423, `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"fix the parser test"},"_meta":{"modelId":"grok-build","promptIndex":0}}`),
		rec(1791368424, `{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"run_terminal_command","rawInput":{"command":"go test ./parser/...","description":"run the parser tests"},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"execute","namespace":"grok_build","label":"Run Command","read_only":false}}}`),
		rec(1791368424, `{"sessionUpdate":"tool_call_update","toolCallId":"call_1","kind":"execute","title":"Execute `+"`go test ./parser/...`"+`","content":[{"type":"content","content":{"type":"text","text":"run the parser tests"}}],"locations":[],"rawInput":{"variant":"Bash","command":"go test ./parser/...","description":"run the parser tests","is_background":false},"_meta":{"x.ai/tool":{"version":1,"name":"run_terminal_command","kind":"execute"}}}`),
		rec(1791368425, `{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"--- FAIL: TestParseSeed (0.00s)\n    parse_test.go:5: want 3, got 4\nFAIL\nFAIL\texample.com/ws/parser\t0.210s\nFAIL\n"}}],"rawOutput":{"type":"Bash","exit_code":1,"command":"go test ./parser/...","truncated":false,"timed_out":false}}`),
		rec(1791368425, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."},"_meta":{"promptIndex":0}}`),
		rec(1791368425, `{"sessionUpdate":"turn_completed","prompt_id":"p1","stop_reason":"end_turn","elapsed_ms":1167}`),
	}
	path := filepath.Join(dir, "updates.jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func jsonInt(n int64) string { return strconv.FormatInt(n, 10) }

// Grok Build's PreCompact names updates.jsonl in both spellings. Grok throws
// away what the prompt hook prints, so the packet goes out on the next
// PreToolUse, whose additionalContext Grok hands the model with the result.
func TestGrokCompactionPacketRidesTheNextToolCall(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	// The store where Grok keeps it, under the home: Grok names the file by
	// the path it resolved, which on macOS is /private/var/... for a home
	// under /var, and a root spelled through the link did not match it.
	t.Setenv("DEJA_GROK_ROOT", "")
	t.Setenv("GROK_HOME", "")
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "66666666-6666-4666-8666-666666666666"
	path := grokUpdates(t, workspace, id)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	pre := mustMarshal(t, map[string]any{
		"hookEventName": "pre_compact", "sessionId": id, "cwd": workspace, "workspaceRoot": workspace + "/",
		"transcriptPath": path, "permissionMode": "default", "source": "manual",
		"hook_event_name": "PreCompact", "session_id": id, "transcript_path": path,
	})
	t.Setenv("GROK_HOOK_EVENT", "pre_compact")
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	state, found, err := index.Compaction(dir, id, workspace)
	if err != nil || !found {
		t.Fatalf("precompact stored nothing for a Grok session: %v", err)
	}
	if state.Harness != "grok" {
		t.Errorf("the capture is filed under %q, want grok", state.Harness)
	}

	// The prompt hook answers nothing on Grok: what it has waits for the
	// session's next tool hook (hook_deferred.go).
	t.Setenv("GROK_HOOK_EVENT", "user_prompt_submit")
	prompt := mustMarshal(t, map[string]any{"sessionId": id, "session_id": id, "cwd": workspace, "prompt": "carry on"})
	withHookStdin(t, string(prompt))
	if out := captureStdout(t, func() { _ = runHookDeferred(dir, "hook-prompt", nil, commands["hook-prompt"]) }); strings.Contains(out, "additionalContext") {
		t.Fatalf("Grok's prompt hook answered: %s", out)
	}

	t.Setenv("GROK_HOOK_EVENT", "pre_tool_use")
	tool := mustMarshal(t, map[string]any{
		"hookEventName": "pre_tool_use", "sessionId": id, "cwd": workspace, "workspaceRoot": workspace + "/",
		"transcriptPath": path, "toolName": "run_terminal_command", "toolUseId": "call_2",
		"toolInput":       map[string]any{"command": "go test ./parser/...", "description": "run it"},
		"hook_event_name": "PreToolUse", "session_id": id, "transcript_path": path,
		"tool_name": "run_terminal_command", "tool_input": map[string]any{"command": "go test ./parser/...", "description": "run it"}, "tool_use_id": "call_2",
	})
	withHookStdin(t, string(tool))
	out := captureStdout(t, func() { _ = runHookDeferred(dir, "hook-tool", nil, commands["hook-tool"]) })
	var response sessionStartHookResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("PreToolUse reply: %v %s", err, out)
	}
	if response.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("reply names %q", response.HookSpecificOutput.HookEventName)
	}
	wantCompactionPacket(t, response.HookSpecificOutput.AdditionalContext)
}

func hostsPrompt(t *testing.T, dir string, payload map[string]any) string {
	t.Helper()
	b := mustMarshal(t, payload)
	var out bytes.Buffer
	if err := runHookPrompt(dir, bytes.NewReader(b), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func hostsNoWarmup(t *testing.T) {
	t.Helper()
	old := spawnWarmup
	spawnWarmup = func(string, string) error { return nil }
	t.Cleanup(func() { spawnWarmup = old })
}

// geminiChat writes a Gemini CLI 0.60 transcript in the layout the CLI keeps,
// with the project folder recorded the way Gemini records it.
func geminiChat(t *testing.T, workspace string, compacted bool) string {
	t.Helper()
	idDir := filepath.Join(t.TempDir(), "tmp", "proj")
	if err := os.MkdirAll(filepath.Join(idDir, "chats"), 0o755); err != nil {
		t.Fatal(err)
	}
	compactionWrite(t, filepath.Join(idDir, ".project_root"), workspace)
	lines := []string{
		`{"sessionId":"gem-1","projectHash":"h","startTime":"2026-10-03T11:49:20Z","lastUpdated":"2026-10-03T11:49:20Z"}`,
		`{"$set":{"messages":[{"id":"ctx","type":"user","content":[{"text":"<session_context>\nsetup"}]}]}}`,
		`{"id":"u1","timestamp":"2026-10-03T11:49:21Z","type":"user","content":[{"text":"fix the parser test"}]}`,
		`{"id":"g1","timestamp":"2026-10-03T11:49:22Z","type":"gemini","content":"","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"go test ./parser/..."},"result":[{"functionResponse":{"id":"c1","name":"run_shell_command","response":{"output":"Output: --- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nExit Code: 1"}}}],"status":"success"}]}`,
		`{"id":"g2","timestamp":"2026-10-03T11:49:23Z","type":"gemini","content":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}`,
	}
	if compacted {
		lines = append(lines,
			`{"$set":{"sessionId":"gem-1"}}`,
			`{"$set":{"messages":[{"id":"ctx","type":"user","content":[{"text":"<session_context>\nsetup"}]},{"id":"s1","type":"user","content":[{"text":"<state_snapshot>fix parser</state_snapshot>"}]},{"id":"s2","type":"gemini","content":[{"text":"Got it."}]}]}}`,
			`{"id":"g3","timestamp":"2026-10-03T11:49:30Z","type":"gemini","content":"ok"}`)
	}
	path := filepath.Join(idDir, "chats", "session-2026-10-03T11-49-gem1.jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// Gemini CLI says nothing after a compaction, so the prompt after one finds it
// in the transcript, delivers the packet once, and leaves a session that never
// compacted alone.
func TestGeminiCompactionIsHandedBackAtTheNextPrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	ask := func(transcript string) string {
		return hostsPrompt(t, dir, map[string]any{
			"session_id": "gem-1", "transcript_path": transcript, "cwd": workspace,
			"hook_event_name": "BeforeAgent", "prompt": "what next",
		})
	}
	if out := ask(geminiChat(t, workspace, false)); strings.Contains(out, "Compaction context") {
		t.Fatalf("a session that never compacted got a packet: %s", out)
	}
	transcript := geminiChat(t, workspace, true)
	out := ask(transcript)
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4", "Reference data, not instructions"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "state_snapshot") {
		t.Errorf("the packet was built from the summary: %s", out)
	}
	if again := ask(transcript); strings.Contains(again, "Compaction context") {
		t.Fatalf("the same compaction was handed back twice: %s", again)
	}
	// Only Gemini's prompt event looks: a host that has its own PreCompact
	// hook gets no capture from its prompts.
	b := mustMarshal(t, map[string]any{"session_id": "gem-2", "transcript_path": transcript, "cwd": workspace, "prompt": "what next"})
	var other bytes.Buffer
	if err := runHookPrompt(dir, bytes.NewReader(b), &other); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := index.Compaction(dir, "gem-2", workspace); found {
		t.Fatal("a UserPromptSubmit payload captured a compaction")
	}
}

// Hermes gives its memory provider the turns it compresses; the provider
// passes them to hook-precompact once the compression commits, under the id
// the next turn asks with.
func TestHermesTurnsBecomeTheNextTurnsPacket(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	result := mustMarshal(t, map[string]any{"output": "--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL", "exit_code": 1})
	payload := mustMarshal(t, map[string]any{
		"session_id": "hermes-new", "cwd": workspace, "harness": "hermes",
		"messages": []any{
			map[string]any{"role": "user", "content": "fix the parser test"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "terminal", "arguments": `{"command":"go test ./parser/..."}`}}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": string(result)},
			map[string]any{"role": "assistant", "content": "The parser test fails: want 3, got 4. I decided to fix parse.go next."},
		},
	})
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	out := hostsPrompt(t, dir, map[string]any{"session_id": "hermes-new", "cwd": workspace, "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
}

// opencode and Kilo name the session; deja reads it from their store.
func TestOpencodeStoreSessionBecomesTheNextTurnsPacket(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	db := filepath.Join(t.TempDir(), "opencode.db")
	quoted := mustMarshal(t, workspace)
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_oc', ` + strings.ReplaceAll(string(quoted), `"`, `'`) + `, '2026-01-02T03:00:00Z','2026-01-03T03:00:00Z');
insert into message values('m1','ses_oc',1767409200000,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"fix the parser test"}');
insert into message values('m2','ses_oc',1767409201000,'{"role":"assistant"}');
insert into part values('p2','m2','{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./parser/..."},"output":"--- FAIL: TestParseSeed\nwant 3, got 4","metadata":{"exit":1}},"time":{"start":"2026-01-02T03:00:01Z"}}');
insert into part values('p3','m2','{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite setup: %v %s", err, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)
	payload := mustMarshal(t, map[string]any{"session_id": "ses_oc", "cwd": workspace, "harness": "opencode"})
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	out := hostsPrompt(t, dir, map[string]any{"session_id": "ses_oc", "cwd": workspace, "prompt": "Continue if you have next steps"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("packet lacks %q: %s", want, out)
		}
	}
}

// The plugins name the session and the store; the compaction's own request
// gets neither recall nor the packet.
func TestOpencodeShapedPluginsSendTheCompactingSession(t *testing.T) {
	for target, js := range map[string]string{
		"opencode": opencodeLegacyPluginJS("/bin/deja"),
		"kilocode": kilocodePluginJS("/bin/deja"),
	} {
		for _, want := range []string{
			`const HARNESS = "` + target + `"`,
			`session_id: sessionID, cwd, harness: HARNESS`,
			`if (owner && compacting.has(owner)) return`,
			`if (input.sessionID && compacting.delete(input.sessionID)) return`,
		} {
			if !strings.Contains(js, want) {
				t.Errorf("%s plugin lacks %q", target, want)
			}
		}
	}
	if js := opencodePluginJS("/bin/deja"); !strings.Contains(js, `session_id: event?.sessionID || "", cwd, harness: "opencode"`) {
		t.Error("the 2.x plugin does not name the compacting session")
	}
	py := hermesCatalogPy()
	for _, want := range []string{"def on_pre_compress", `kwargs.get("reason") == "compression"`, `"harness": "hermes"`} {
		if !strings.Contains(py, want) {
			t.Errorf("hermes provider lacks %q", want)
		}
	}
}

// kimiCompactionSession writes a Kimi Code 0.28.1 session the way the CLI
// lays it out: wire.jsonl under agents/main, the working directory in the
// state.json two levels up. The records are the ones a stand run wrote, with
// the long system prompt and tool catalogue left out.
func kimiCompactionSession(t *testing.T, workspace, id string) {
	t.Helper()
	session := filepath.Join(sources.KimiRoot(), "sessions", "wd_work_50ec6d22c9dc", id)
	state := mustMarshal(t, map[string]any{
		"createdAt": "2026-10-07T10:16:41.259Z", "updatedAt": "2026-10-07T10:16:57.256Z",
		"title": "fix the parser test", "workDir": workspace,
		"agents": map[string]any{"main": map[string]any{"type": "main", "parentAgentId": nil}},
	})
	compactionWrite(t, filepath.Join(session, "state.json"), string(state))
	compactionWrite(t, filepath.Join(session, "agents", "main", "wire.jsonl"), strings.Join([]string{
		`{"type":"metadata","protocol_version":"1.4","created_at":1791368201286}`,
		`{"type":"config.update","modelAlias":"mock","thinkingEffort":"off","time":1791368201288}`,
		`{"type":"turn.prompt","input":[{"type":"text","text":"fix the parser test"}],"origin":{"kind":"user"},"time":1791368201401}`,
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1791368201402}`,
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1","turnId":"0","step":1},"time":1791368201541}`,
		`{"type":"context.append_loop_event","event":{"type":"tool.call","uuid":"call_1","turnId":"0","step":1,"stepUuid":"s1","toolCallId":"call_1","name":"Bash","args":{"command":"go test ./parser/..."}},"time":1791368201560}`,
		`{"type":"context.append_loop_event","event":{"type":"tool.result","parentUuid":"call_1","toolCallId":"call_1","result":{"output":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\nCommand failed with exit code: 1.","isError":true}},"time":1791368201580}`,
		`{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1","turnId":"0","step":1,"finishReason":"tool_use"},"time":1791368201581}`,
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2","turnId":"0","step":2},"time":1791368201590}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}},"time":1791368201600}`,
		`{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","turnId":"0","step":2,"finishReason":"end_turn"},"time":1791368201610}`,
	}, "\n")+"\n")
}

// Kimi Code waits for PreCompact before it compacts, and the payload names
// only the session: no transcript, no harness. The hook entry says whose it is
// (--harness kimi), the session's wire.jsonl is found by its id, and the
// packet rides the next prompt. The payload is the one Kimi 0.28.1 sent on a
// stand.
func TestKimiCompactionIsReadByTheSessionID(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	out := precompactThen(t, dir, workspace, map[string]any{
		"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace,
		"trigger": "auto", "token_count": 216,
	}, "kimi")
	wantCompactionPacket(t, out)
	if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != "kimi" {
		t.Errorf("the capture is filed under %q, want kimi", state.Harness)
	}
}

// Without the harness the payload names nothing deja can read, which is what
// the hook entry before --harness got: the forget and no capture.
func TestKimiCompactionNeedsTheHarnessFromTheHookEntry(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	b := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace, "trigger": "auto"})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "")
	if _, found, _ := index.Compaction(dir, id, workspace); found {
		t.Fatal("captured a Kimi session nobody named the harness of")
	}
}

// Kimi runs hook-context --once and hook-prompt on the same prompt, in
// parallel; on a stand the packet came back from both. Only the prompt hook
// carries it.
func TestKimiOnceDigestLeavesThePacketToThePromptHook(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "session_dedaa2f2-4daf-4366-b40e-f1dd5ec48842"
	kimiCompactionSession(t, workspace, id)
	b := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "session_id": id, "cwd": workspace})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "kimi")
	prompt := mustMarshal(t, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "cwd": workspace, "prompt": "what next"})
	withHookStdin(t, string(prompt))
	if out := captureStdout(t, func() { _ = runHookContextMode(dir, true, true) }); strings.Contains(out, "Compaction context") {
		t.Fatalf("hook-context --once handed the packet out: %s", out)
	}
	wantCompactionPacket(t, hostsPrompt(t, dir, map[string]any{"session_id": id, "cwd": workspace, "prompt": "what next"}))
}

// The kimi-auto block passes --harness kimi on its PreCompact entry.
func TestKimiAutoNamesTheHarnessOnPreCompact(t *testing.T) {
	hermeticEnv(t)
	r, err := installKimiAuto("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readConfig(r.Path)
	if !strings.Contains(string(b), `hook-precompact --harness kimi"`) {
		t.Fatalf("the PreCompact entry does not name kimi:\n%s", b)
	}
}

// A forked agent that compacts runs PreCompact under its parent's session id,
// and the forget that follows took the parent's delivery mark with it: the
// parent's packet from a day earlier, naming PRs merged since and a request
// many turns old, was delivered again on the parent's next prompt.
func TestForgetKeepsThePacketDeliveryMark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	rememberInjectedIDsFor(dir, "parent", "proj", []string{"some-session", "compaction:abc123"})
	rememberInjectedIDs(dir, compactionFailureKey("parent"), compactionUnavailableToken("/w"))

	forgetInjected(dir, "parent")
	forgetInjected(dir, compactionFailureKey("parent"))

	got := alreadyInjected(dir, "parent")
	if !got["compaction:abc123"] {
		t.Fatalf("the delivery mark went with the forget: %v", got)
	}
	if got["some-session"] {
		t.Fatalf("the session's recall rows survived the forget: %v", got)
	}
	if len(alreadyInjected(dir, compactionFailureKey("parent"))) != 0 {
		t.Fatal("the failure mark must still clear, so a new capture can be delivered")
	}
}

// A rotation keeps the caller's lines and the recent tail. A delivery mark
// written days earlier by a long session is in neither when another session
// rotates the file.
func TestRotationKeepsCompactionMarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	p := dir + ".hookseen"
	var b strings.Builder
	b.WriteString("parent compaction:abc123 2026-10-03T12:08:57Z proj\n")
	for i := range 6000 {
		fmt.Fprintf(&b, "other-%d session-%d 2026-10-04T00:00:00Z proj\n", i%50, i)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateHookseen(p, "someone-else")
	if !alreadyInjected(dir, "parent")["compaction:abc123"] {
		t.Fatal("rotation dropped the packet's delivery mark")
	}
	if got := len(loadSeen(dir)); got > 4100 {
		t.Fatalf("rotation kept %d lines", got)
	}
}

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

// Muse Code fires PreCompact before every compaction with transcript_path null
// and no harness in the payload, and nothing after it that takes context:
// PostCompact rejects additionalContext and SessionStart does not fire again.
// The log is found by the session id, and the packet goes out on the next
// prompt or edit (1.4.1 and 1.4.2, measured, #4737).
func TestMuseCompactionPacketReachesTheNextPrompt(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "01a10bdb-0147-7f40-99e9-cc17071cd6e8"
	rec := func(n int, typ string, payload any) string {
		b := mustMarshal(t, map[string]any{
			"schema_version": 1, "id": "r", "stream": map[string]any{"kind": "session", "id": id},
			"sequence": n, "recorded_at": int64(1_791_200_000_000_000) + int64(n)*1_000_000,
			"record_type": "event", "payload_type": typ, "payload": payload,
		})
		return string(b)
	}
	run := func(n int, ev map[string]any) string {
		return rec(n, "runtime.session", map[string]any{"kind": "run", "event": ev})
	}
	compactionWrite(t, filepath.Join(sources.MuseRoot(), "2026", "10", "05", id, "session.jsonl"), strings.Join([]string{
		rec(1, "runtime.session.metadata", map[string]any{"record": map[string]any{"workspace_root": workspace}}),
		run(2, map[string]any{"kind": "started", "prompt": "fix the parser test"}),
		run(3, map[string]any{"kind": "assistant_tool_calls_committed", "tool_calls": []any{
			map[string]any{"call_id": "call_1", "name": "bash", "args": `{"command":"go test ./parser/..."}`},
		}}),
		run(4, map[string]any{"kind": "tool_result_batch_committed", "results": []any{
			map[string]any{"tool_call_id": "call_1", "text": `{"command":"go test ./parser/...","exit_code":1,"output":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n"}`},
		}}),
		run(5, map[string]any{"kind": "assistant_message_committed", "text": "The parser test fails: want 3, got 4. I decided to fix parse.go next."}),
	}, "\n")+"\n")

	pre := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "trigger": "hard", "session_id": id, "cwd": workspace, "transcript_path": nil, "model_provider": "meta"})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, err := index.Compaction(dir, id, workspace); err != nil || !found {
		t.Fatalf("precompact stored nothing for a Muse session: %v", err)
	}
	got := hostsPrompt(t, dir, map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": id, "cwd": workspace, "transcript_path": nil, "prompt": "what next"})
	for _, want := range []string{"Compaction context", "go test ./parser/...", "[failed]", "want 3, got 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt packet lacks %q: %s", want, got)
		}
	}
}

// Another session's log under the same id is not this one: the reader checks
// the log's own stream.
func TestMuseCompactionRefusesALogOfAnotherSession(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	const id = "01a10bdb-0147-7f40-99e9-cc17071cd6e8"
	b := mustMarshal(t, map[string]any{"stream": map[string]any{"kind": "session", "id": "01a10bdb-0000-7000-8000-000000000000"},
		"recorded_at": 1_791_200_000_000_000, "payload_type": "runtime.session",
		"payload": map[string]any{"kind": "run", "event": map[string]any{"kind": "started", "prompt": "someone else's work"}}})
	compactionWrite(t, filepath.Join(sources.MuseRoot(), "2026", "10", "05", id, "session.jsonl"), string(b)+"\n")
	pre := mustMarshal(t, map[string]any{"hook_event_name": "PreCompact", "trigger": "hard", "session_id": id, "cwd": workspace})
	withHookStdin(t, string(pre))
	runHookPrecompact(dir)
	if _, found, _ := index.Compaction(dir, id, workspace); found {
		t.Fatal("captured a log whose stream names another session")
	}
}

// openclawSession writes an OpenClaw 2026.7.1-2 session the way its local agent
// does: a pi-shaped JSONL under agents/<agent>/sessions/<id>.jsonl whose header
// records the agent's workspace, an exec call that failed, and the compaction
// entry a run appends once it has summarised the turns before it.
func openclawSession(t *testing.T, id, workspace string) string {
	t.Helper()
	cwd := mustMarshal(t, workspace)
	path := filepath.Join(sources.OpenClawRoot(), "main", "sessions", id+".jsonl")
	compactionWrite(t, path, strings.Join([]string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:20:45.000Z","cwd":` + string(cwd) + `}`,
		`{"type":"model_change","id":"31fcff3c","parentId":null,"timestamp":"2026-10-07T10:20:45.010Z","provider":"stub","modelId":"stub-model"}`,
		`{"type":"message","id":"17cbcb01","parentId":"31fcff3c","timestamp":"2026-10-07T10:20:46.612Z","message":{"role":"user","content":"fix the parser test","timestamp":1791368445245}}`,
		`{"type":"message","id":"2b64c72e","parentId":"17cbcb01","timestamp":"2026-10-07T10:20:46.656Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"exec","arguments":{"command":"go test ./parser/..."}}],"api":"openai-completions","provider":"stub","model":"stub-model","stopReason":"toolUse","timestamp":1791368446616}}`,
		`{"type":"message","id":"0596fb6b","parentId":"2b64c72e","timestamp":"2026-10-07T10:20:46.841Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"exec","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n(Command exited with code 1)"}],"details":{"status":"completed","exitCode":1,"cwd":` + string(cwd) + `},"isError":true,"timestamp":1791368446840}}`,
		`{"type":"message","id":"28036307","parentId":"0596fb6b","timestamp":"2026-10-07T10:20:46.868Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"stopReason":"stop","timestamp":1791368446849}}`,
		`{"type":"compaction","id":"891d328a","parentId":"28036307","timestamp":"2026-10-07T10:20:46.908Z","summary":"done","firstKeptEntryId":"28036307","tokensBefore":31001,"details":{"readFiles":[],"modifiedFiles":[]},"fromHook":false}`,
	}, "\n")+"\n")
	return path
}

// OpenClaw compacts two ways, measured on 2026.7.1-2. `openclaw sessions
// compact` runs through the gateway, whose before_compaction names the session
// id and the workspace but no file; a run that fills its window compacts on its
// own and names the file and the session key only. Either way the session file
// still holds the turns, and the next turn gets the packet.
func TestOpenClawCompactionPacketReachesTheNextTurn(t *testing.T) {
	for name, file := range map[string]bool{"gateway, by id": false, "in the run, by file": true} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(t.TempDir(), "openclaw"))
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			path := openclawSession(t, "sc1", workspace)
			payload := map[string]any{"session_id": "sc1", "transcript_path": "", "cwd": workspace, "harness": "openclaw"}
			if file {
				payload["transcript_path"] = path
			}
			out := precompactThen(t, dir, workspace, payload, "")
			wantCompactionPacket(t, out)
			state, _, _ := index.Compaction(dir, "sc1", workspace)
			if state.Harness != "openclaw" {
				t.Errorf("the capture is filed under %q, want openclaw", state.Harness)
			}
		})
	}
}

// The plugin names the agent's workspace, which is what the session header
// records, rather than the gateway's own directory; and the compaction a run
// starts on its own, whose ctx holds only the session key, goes out under the
// session id and workspace that key last ran with.
func TestOpenClawPluginNamesTheSessionItCompacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub deja is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the plugin openclaw would run")
	}
	home := t.TempDir()
	stub := filepath.Join(home, "deja")
	calls := filepath.Join(home, "calls")
	script := "#!/bin/sh\nin=$(cat)\nprintf '%s\\t%s\\n' \"$1\" \"$in\" >> " + calls + "\n"
	if err = os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, "index.mjs")
	if err = os.WriteFile(plugin, []byte(openclawPluginJS(stub)), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
import plugin from "` + plugin + `";
const h = {};
plugin.register({ on: (name, fn) => { h[name] = fn } });
const key = "agent:main:explicit:sa1";
await h.before_prompt_build({ prompt: "fix the parser test", messages: [] }, { sessionKey: key, sessionId: "sa1", workspaceDir: "/w/agent-workspace" });
await h.before_compaction({ messageCount: 4, messages: [], sessionFile: "/s/sa1.jsonl" }, { sessionKey: key });
`
	run := filepath.Join(home, "drive.mjs")
	if err = os.WriteFile(run, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, runErr := exec.CommandContext(t.Context(), node, run).CombinedOutput(); runErr != nil {
		t.Fatalf("driving the plugin: %v\n%s", runErr, out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		sub, body, _ := strings.Cut(l, "\t")
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("payload %q: %v", l, err)
		}
		got[sub] = m
	}
	if cwd := got["hook-prompt"]["cwd"]; cwd != "/w/agent-workspace" {
		t.Errorf("hook-prompt was sent cwd %v, not the agent's workspace", cwd)
	}
	pre := got["hook-precompact"]
	if pre["session_id"] != "sa1" || pre["cwd"] != "/w/agent-workspace" || pre["transcript_path"] != "/s/sa1.jsonl" || pre["harness"] != "openclaw" {
		t.Errorf("the in-run compaction went out as %v", pre)
	}
}

// piSessionFile writes a session the way pi 0.73 and omp 17.4 write one,
// copied from a stand run: header, the failing command and its result, and the
// `compaction` entry session_compact fires after. The turns before that entry
// stay in the file, which is what the capture reads.
func piSessionFile(t *testing.T, root, workspace, id string, omp bool) string {
	t.Helper()
	cwd := mustMarshal(t, workspace)
	attribution := ""
	if omp {
		attribution = `"attribution":"user",`
	}
	lines := []string{
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:15:35.201Z","cwd":` + string(cwd) + `}`,
		`{"type":"model_change","id":"5d90ad3f","parentId":null,"timestamp":"2026-10-07T10:15:35.252Z","provider":"mock","modelId":"mock-model"}`,
		`{"type":"message","id":"fb64858b","parentId":"5d90ad3f","timestamp":"2026-10-07T10:15:35.671Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],` + attribution + `"timestamp":1791368135631}}`,
		`{"type":"message","id":"f91894f3","parentId":"fb64858b","timestamp":"2026-10-07T10:15:35.726Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"go test ./parser/..."}}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"toolUse","timestamp":1791368135700}}`,
		`{"type":"message","id":"d5d9ae2b","parentId":"f91894f3","timestamp":"2026-10-07T10:15:35.773Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n\nCommand exited with code 1"}],"isError":true,"timestamp":1791368135770}}`,
		`{"type":"message","id":"a48b8c07","parentId":"d5d9ae2b","timestamp":"2026-10-07T10:15:35.777Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"stop","timestamp":1791368135775}}`,
		`{"type":"compaction","id":"e8d9e4dc","parentId":"a48b8c07","timestamp":"2026-10-07T10:15:35.843Z","summary":"Fixing the parser test.","firstKeptEntryId":"a48b8c07","tokensBefore":0,"details":{"readFiles":[],"modifiedFiles":[]},"fromHook":false}`,
	}
	if omp {
		lines = append([]string{`{"type":"title","v":1,"title":"","updatedAt":"2026-10-07T10:15:35.201Z","pad":"    "}`}, lines...)
	}
	path := filepath.Join(root, "--"+strings.Trim(encodeLikeClaude(workspace), "-")+"--", "2026-10-07T10-15-35-201Z_"+id+".jsonl")
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// pi's session_compact fires once the compaction entry is in the file; the
// extension sends the session file it names, and the next prompt carries the
// packet. omp sends the same, and its own name, from the same event.
func TestPiFamilyCompactionIsReadFromTheSessionFile(t *testing.T) {
	for _, host := range []string{"pi", "omp"} {
		t.Run(host, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			const id = "01a115dc-8221-70f9-956a-50742150659d"
			root, payload := sources.PiRoot(), map[string]any{"session_id": id, "cwd": workspace}
			if host == "omp" {
				root, payload["harness"] = sources.OmpRoot(), "omp"
			}
			payload["transcript_path"] = piSessionFile(t, root, workspace, id, host == "omp")
			wantCompactionPacket(t, precompactThen(t, dir, workspace, payload, ""))
			if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != host {
				t.Errorf("the capture is filed under %q, want %s", state.Harness, host)
			}
		})
	}
}

// The file is only the place to look: a session file whose header names
// another session is not this one.
func TestPiCompactionRefusesAnotherSessionsFile(t *testing.T) {
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	path := piSessionFile(t, sources.PiRoot(), workspace, "01a115dc-0000-7000-8000-000000000000", false)
	b := mustMarshal(t, map[string]any{"session_id": "01a115dc-8221-70f9-956a-50742150659d", "transcript_path": path, "cwd": workspace})
	withHookStdin(t, string(b))
	runHookPrecompactFor(dir, "")
	if _, found, _ := index.Compaction(dir, "01a115dc-8221-70f9-956a-50742150659d", workspace); found {
		t.Fatal("captured a session file that belongs to another session")
	}
}

// The extensions read the session file off the session manager on the
// compaction event itself, which pi and omp both hand ctx.
func TestPiFamilyExtensionsSendTheSessionFile(t *testing.T) {
	for name, src := range map[string]string{
		"pi":    piExtensionTS("/bin/deja"),
		"omp":   ompExtensionJS("/bin/deja"),
		"prime": primeExtensionTS("/bin/deja"),
	} {
		for _, want := range []string{"m.getSessionFile ? m.getSessionFile() : m.sessionFile", "transcript_path: sessionFile"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s extension lacks %q", name, want)
			}
		}
		if !strings.Contains(src, `pi.on("session_compact", async (_event`) || !strings.Contains(src, "remember(ctx);\n      run([\"hook-precompact\"]") {
			t.Errorf("%s extension does not read the session on session_compact", name)
		}
	}
}

// pifamilySessionFile writes a session the way Senpi 2026.10.10-5, gjc 0.18.7
// and prime-agent 0.9.8 wrote one on a stand: pi's header and entries, each
// host's own extras around them, and the `compaction` entry session_compact
// fires after. The turns before that entry stay in the file.
func pifamilySessionFile(t *testing.T, host, workspace, id string) string {
	t.Helper()
	cwd := mustMarshal(t, workspace)
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:18:19.235Z","cwd":` + string(cwd) + `}`
	call := `{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"go test ./parser/..."}}`
	result := `{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"--- FAIL: TestParseSeed\nwant 3, got 4\nFAIL\n\n\nCommand exited with code 1"}],"details":{},"isError":true,"timestamp":1791368300097}`
	var extra []string
	var path string
	switch host {
	case "senpi":
		extra = []string{
			`{"type":"custom","customType":"pi-rules.scan","data":{"cwd":` + string(cwd) + `,"reason":"startup"},"id":"f92033d0","parentId":"015e1f06","timestamp":"2026-10-07T10:18:19.462Z"}`,
			`{"type":"custom_message","customType":"environment-context","content":"<environment_context>\n  <cwd>` + workspace + `</cwd>\n</environment_context>","display":false,"id":"58d9a505","parentId":"f92033d0","timestamp":"2026-10-07T10:18:20.086Z"}`,
		}
		path = filepath.Join(sources.SenpiRoot(), "--"+strings.Trim(encodeLikeClaude(workspace), "-")+"--", "2026-10-07T10-18-19-235Z_"+id+".jsonl")
	case "gjc":
		path = filepath.Join(sources.GjcRoot(), "v2-43csdzcwfkots3utpzkeudsc75bj7d4uqpj4csv5ax2hlkqsrrwq", "2026-10-07T10-31-25-797Z_"+id+".jsonl")
	case "prime":
		// prime's header records the depth and the git state; its one tool
		// is ipython, and a cell's shell result comes back as a BashResult.
		header = `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-07T10:24:22.227Z","cwd":` + string(cwd) + `,"rlmDepth":0,"git":{"commit":"cc5a71a13a6672cc3b84a085c912b969a09389c5","branch":"main"}}`
		call = `{"type":"toolCall","id":"call_1","name":"ipython","arguments":{"code":"await bash(\"go test ./parser/...\")"}}`
		result = `{"role":"toolResult","toolCallId":"call_1","toolName":"ipython","content":[{"type":"text","text":"BashResult(exit_code=1, output='--- FAIL: TestParseSeed\\nwant 3, got 4\\nFAIL\\n', duration=0.25)\n"}],"isError":false,"timestamp":1791368300097}`
		path = filepath.Join(sources.PrimeRoot(), id+".jsonl")
	}
	lines := append([]string{
		header,
		`{"type":"model_change","id":"94cf0d5a","parentId":null,"timestamp":"2026-10-07T10:18:19.429Z","provider":"mock","modelId":"mock-model"}`,
		`{"type":"thinking_level_change","id":"015e1f06","parentId":"94cf0d5a","timestamp":"2026-10-07T10:18:19.429Z","thinkingLevel":"off"}`,
	}, extra...)
	lines = append(lines,
		`{"type":"message","id":"9f5237af","parentId":"58d9a505","timestamp":"2026-10-07T10:18:20.086Z","message":{"role":"user","content":[{"type":"text","text":"fix the parser test"}],"timestamp":1791368299936}}`,
		`{"type":"message","id":"ce5c9415","parentId":"9f5237af","timestamp":"2026-10-07T10:18:20.096Z","message":{"role":"assistant","content":[`+call+`],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"toolUse","timestamp":1791368300090}}`,
		`{"type":"message","id":"3d39c20c","parentId":"ce5c9415","timestamp":"2026-10-07T10:18:20.097Z","message":`+result+`}`,
		`{"type":"message","id":"ed73bd4e","parentId":"3d39c20c","timestamp":"2026-10-07T10:18:20.103Z","message":{"role":"assistant","content":[{"type":"text","text":"The parser test fails: want 3, got 4. I decided to fix parse.go next."}],"api":"openai-completions","provider":"mock","model":"mock-model","stopReason":"stop","timestamp":1791368300100}}`,
		`{"type":"compaction","id":"9ecc269c","parentId":"ed73bd4e","timestamp":"2026-10-07T10:18:20.112Z","summary":"Fixing the parser test.","firstKeptEntryId":"ed73bd4e","tokensBefore":182,"details":{"readFiles":[],"modifiedFiles":[]}}`,
	)
	compactionWrite(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// Senpi and gjc run pi's own extension, which sends the session file
// session_compact names; prime-agent's sends it with its name. The next prompt
// carries the packet, filed under the host whose store holds the file.
func TestPiDescendantsCompactionIsReadFromTheSessionFile(t *testing.T) {
	for _, host := range []string{"senpi", "gjc", "prime"} {
		t.Run(host, func(t *testing.T) {
			hermeticEnv(t)
			hostsNoWarmup(t)
			t.Setenv("SENPI_CODING_AGENT_DIR", "")
			t.Setenv("GJC_CODING_AGENT_DIR", "")
			t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "")
			t.Setenv("PRIME_AGENT_SESSION_DIR", "")
			t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", "")
			workspace := compactionGitRepo(t)
			dir := index.DefaultDir()
			const id = "01a115df-02e3-778b-85fd-a6c895d75e88"
			payload := map[string]any{"session_id": id, "cwd": workspace, "transcript_path": pifamilySessionFile(t, host, workspace, id)}
			if host == "prime" {
				payload["harness"] = "prime"
			}
			out := precompactThen(t, dir, workspace, payload, "")
			if host == "prime" {
				// prime runs commands inside an ipython cell, which the reader
				// does not split into commands; the turn and the conclusion
				// still come back.
				for _, want := range []string{"Compaction context", "fix the parser test", "want 3, got 4"} {
					if !strings.Contains(out, want) {
						t.Errorf("packet lacks %q: %s", want, out)
					}
				}
			} else {
				wantCompactionPacket(t, out)
			}
			if state, _, _ := index.Compaction(dir, id, workspace); state.Harness != host {
				t.Errorf("the capture is filed under %q, want %s", state.Harness, host)
			}
		})
	}
}

// The extension a pi descendant runs is pi's, so what it sends on compaction
// is what pi's sends: the session file, not only the id.
func TestPiDescendantExtensionsSendTheSessionFile(t *testing.T) {
	for name, src := range map[string]string{"pi-shaped": piExtensionTS("/bin/deja"), "prime": primeExtensionTS("/bin/deja")} {
		if !strings.Contains(src, `transcript_path: sessionFile`) || !strings.Contains(src, `m.getSessionFile ? m.getSessionFile()`) {
			t.Errorf("%s extension does not send the session file on compaction", name)
		}
	}
}

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
	b := mustMarshal(t, payload)
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
	cwd := mustMarshal(t, workspace)
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
	quoted := mustMarshal(t, workspace)
	rollout := strings.ReplaceAll(string(b), "WORKSPACE_PATH", strings.Trim(string(quoted), `"`))
	if _, err = sources.ReadCompactionTranscript(writeTemp(t, rollout), id); err == nil {
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
	for line := range strings.SplitSeq(strings.TrimSpace(string(journal)), "\n") {
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

func TestDevinPostCompactionCapturesAndEmitsRecoveryPacket(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	hostsNoWarmup(t)
	workspace := compactionGitRepo(t)
	dir := index.DefaultDir()
	db := filepath.Join(t.TempDir(), "cli", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_DEVIN_DB", db)

	// Nodes 1-5 are the pre-compaction chain; node 6 is the rebuilt prefix root
	// and node 7 is the compacted head pointing back via compact/prior_node_ids.
	sql := fmt.Sprintf(`
create table sessions (
    id text primary key, working_directory text, backend_type text, model text,
    agent_mode text, created_at integer, last_activity_at integer, title text,
    main_chain_id integer, hidden integer not null default 0
);
create table message_nodes (
    row_id integer primary key autoincrement, session_id text not null,
    node_id integer not null, parent_node_id integer, chat_message text not null,
    created_at integer, metadata text
);
create table subagent_heads (
    session_id text not null, agent_id text not null, chain_node_id integer not null,
    updated_at integer not null, primary key (session_id, agent_id)
);
create table prompt_history (
    id integer primary key autoincrement, content text, timestamp integer,
    session_id text, is_shell integer not null default 0
);
insert into sessions values ('devin-c1', '%s', 'cli', 'swe-2', 'normal', 1785600000, 1785600090, 'fix retry', 7, 0);
insert into message_nodes (session_id, node_id, parent_node_id, chat_message, created_at, metadata) values
('devin-c1', 1, null, '{"message_id":"s1","role":"system","content":"<system_info>x</system_info>"}', 1785600001, '{"is_system_prefix":true}'),
('devin-c1', 2, 1, '{"message_id":"u1","role":"user","content":"fix the retry loop in retry.go"}', 1785600002, null),
('devin-c1', 3, 2, '{"message_id":"a1","role":"assistant","content":"editing retry.go","tool_calls":[{"id":"c1","name":"edit","arguments":{"file_path":%q,"old_string":"i < n","new_string":"i <= n"},"index":0,"kind":"function"}]}', 1785600003, null),
('devin-c1', 4, 3, '{"message_id":"t1","role":"tool","tool_call_id":"c1","content":"ok"}', 1785600004, null),
('devin-c1', 5, 4, '{"message_id":"a2","role":"assistant","content":"We decided to bound retry.go with i <= n so the final attempt runs."}', 1785600005, null),
('devin-c1', 6, null, '{"message_id":"s2","role":"system","content":"<system_info>rebuilt</system_info>"}', 1785600010, '{"is_system_prefix":true}'),
('devin-c1', 7, 6, '{"message_id":"u2","role":"user","content":"Summary of prior turns"}', 1785600011, '{"extensions":{"compact/prior_node_ids":[2,3,4,5]}}');
`, strings.ReplaceAll(workspace, "'", "''"), filepath.Join(workspace, "retry.go"))

	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v: %s", err, out)
	}

	rememberInjectedIDs(dir, "devin-c1", "prior-session-hit")
	payload := fmt.Sprintf(`{"hook_event_name":"PostCompaction","session_id":"devin-c1","cwd":%q}`, workspace)
	withHookStdin(t, payload)
	out := captureStdout(t, func() {
		if err := runHookContext(dir, false); err != nil {
			t.Fatal(err)
		}
	})
	if alreadyInjected(dir, "devin-c1")["prior-session-hit"] {
		t.Fatalf("PostCompaction did not clear the seen ledger")
	}
	if !strings.Contains(out, `"hookEventName":"PostCompaction"`) || !strings.Contains(out, "retry.go") {
		t.Fatalf("PostCompaction output missing event name or recovered file:\n%s", out)
	}
}

func compactionFixture(t *testing.T, name, workspace string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "synthetic", "compaction", name))
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustMarshal(t, workspace)
	return strings.ReplaceAll(string(b), `"WORKSPACE"`, string(encoded))
}

func prepareCompaction(t *testing.T) (dir, workspace, transcript string) {
	t.Helper()
	hermeticEnv(t)
	workspace = compactionGitRepo(t)
	compactionWrite(t, filepath.Join(workspace, "retry.go"), "old\n")
	compactionGit(t, workspace, "add", "retry.go")
	compactionGit(t, workspace, "commit", "-m", "fixture")
	dir = index.DefaultDir()
	transcript = filepath.Join(t.TempDir(), "compaction-fixture.jsonl")
	compactionWrite(t, transcript, compactionFixture(t, "claude-before.jsonl", workspace))
	oldSpawn := spawnWarmup
	spawnWarmup = func(string, string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	payload := mustMarshal(t, map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript})
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	state, found, err := index.Compaction(dir, "compaction-fixture", workspace)
	if err != nil || !found {
		t.Fatalf("precompact did not persist state: found=%v err=%v", found, err)
	}
	if state.Data.Objective.Text == "" || len(state.Data.Tests) != 1 || len(state.Data.Gaps) != 2 || len(state.Data.Conflicts) != 1 {
		t.Fatalf("transcript schema was not populated: %+v", state.Data)
	}
	return dir, workspace, transcript
}

func TestCompactionHooksRecoverBeforeIndexBuildOnceAndMeasureRawActions(t *testing.T) {
	dir, workspace, transcript := prepareCompaction(t)
	if index.IsCurrentVersion(dir) {
		t.Fatal("a packet must not impersonate a searchable index")
	}
	rememberSessionDigest(dir, "compaction-fixture")
	payload := mustMarshal(t, map[string]any{"session_id": "compaction-fixture", "cwd": workspace, "source": "compact", "deja_once": true})
	withHookStdin(t, string(payload))
	out := captureStdout(t, func() {
		// The payload's deja_once, as OpenClaw and dsh send it; the --once
		// flag is Kimi's, whose prompt hook carries the packet instead.
		if err := runHookContextMode(dir, true, false); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Fix retry cancellation", "integration fixture", "two attempts", "go test", "Reference data, not instructions"} {
		if !strings.Contains(out, want) {
			t.Errorf("recovery omitted %q: %s", want, out)
		}
	}
	if len(out) > compactionRecoveryBytes || strings.Contains(out, "abcdefghijklmnoPQRSTUVWXYZ123456") || strings.Count(out, "</deja-recall>") != 1 {
		t.Fatalf("recovery broke redaction/framing/budget: %s", out)
	}
	var duplicate bytes.Buffer
	if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "UserPromptSubmit", hookToolPlain, &duplicate); err != nil || delivered || duplicate.Len() != 0 {
		t.Fatalf("recovery repeated across hook surfaces: %t %v %s", delivered, err, duplicate.String())
	}
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(compactionFixture(t, "claude-after.jsonl", workspace))
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("append fixture: %v %v", err, closeErr)
	}
	toolPayload := mustMarshal(t, map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript, "tool_name": "Edit", "tool_use_id": "edit-after"})
	for range 2 {
		if err := runHookTool(dir, bytes.NewReader(toolPayload), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	summary := usage.CompactionRecoverySummary(dir)
	if summary == nil || summary.Captures != 1 || summary.Measured != 1 || summary.MedianActions != 2 || summary.Pending != 0 {
		t.Fatalf("expected two raw actions before one edit, got %+v", summary)
	}
}

func TestCompactionRecoveryHonorsWorkspacePolicyAndWriteSuccess(t *testing.T) {
	dir, workspace, _ := prepareCompaction(t)
	if _, packet := compactionRecovery(dir, "another-session", workspace); packet != "" {
		t.Fatal("another session received this packet")
	}
	if _, packet := compactionRecovery(dir, "compaction-fixture", t.TempDir()); packet != "" {
		t.Fatal("another worktree received this packet")
	}
	t.Setenv("DEJA_RECALL", "off")
	if _, packet := compactionRecovery(dir, "compaction-fixture", workspace); packet != "" {
		t.Fatal("recall off delivered a packet")
	}
	t.Setenv("DEJA_RECALL", "")
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	compactionWrite(t, policyPath, `{"activations":{"auto":{"local":false}}}`)
	t.Setenv("DEJA_POLICY_FILE", policyPath)
	if _, packet := compactionRecovery(dir, "compaction-fixture", workspace); packet != "" {
		t.Fatal("denied local recall delivered a packet")
	}
	compactionWrite(t, policyPath, `{}`)
	if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "PreToolUse", hookToolPlain, compactionBrokenWriter{}); !delivered || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure lost: delivered=%t err=%v", delivered, err)
	}
	compactionWrite(t, filepath.Join(workspace, "retry.go"), "new\n")
	var out bytes.Buffer
	if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "PreToolUse", hookToolCrush, &out); !delivered || err != nil {
		t.Fatalf("failed write consumed packet: delivered=%t err=%v", delivered, err)
	}
	var response struct {
		Context string `json:"context"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || !strings.Contains(response.Context, "Repository changed since compaction") {
		t.Fatalf("changed repository silently reused conclusions: %v %s", err, out.String())
	}
}

type compactionBrokenWriter struct{}

func (compactionBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCompactionCaptureRejectsForeignTranscriptAndRecordsFailure(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	workspace := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "foreign.jsonl")
	compactionWrite(t, transcript, compactionFixture(t, "claude-before.jsonl", t.TempDir()))
	captureCompaction(dir, precompactHookInput{SessionID: "compaction-fixture", CWD: workspace, TranscriptPath: transcript})
	if _, found, err := index.Compaction(dir, "compaction-fixture", workspace); err != nil || found {
		t.Fatalf("foreign workspace captured: found=%t err=%v", found, err)
	}
	summary := usage.CompactionRecoverySummary(dir)
	if summary == nil || summary.Measured != 0 || summary.Unmeasured != 1 {
		t.Fatalf("capture failure was not reported honestly: %+v", summary)
	}
}

func TestCompactionFailedLaterCaptureSuppressesOldPacket(t *testing.T) {
	dir, workspace, transcript := prepareCompaction(t)
	if delivered, err := emitCompactionRecovery(dir, "compaction-fixture", workspace, "SessionStart", hookToolPlain, io.Discard); !delivered || err != nil {
		t.Fatalf("initial recovery: %t %v", delivered, err)
	}
	payload := mustMarshal(t, map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript})
	compactionWrite(t, transcript, "not a supported transcript\n")
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	if _, packet := compactionRecovery(dir, "compaction-fixture", workspace); packet != "" {
		t.Fatalf("failed new capture replayed older state: %s", packet)
	}
	before := strings.ReplaceAll(compactionFixture(t, "claude-before.jsonl", workspace), "Fix retry cancellation", "Repair the new parser")
	compactionWrite(t, transcript, before)
	withHookStdin(t, string(payload))
	runHookPrecompact(dir)
	if _, packet := compactionRecovery(dir, "compaction-fixture", workspace); !strings.Contains(packet, "Repair the new parser") {
		t.Fatalf("successful later capture remained suppressed: %s", packet)
	}
}

func TestCompactionRecoveryUsesExistingPromptAndToolEnvelopes(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "PreToolUse"} {
		t.Run(event, func(t *testing.T) {
			dir, workspace, _ := prepareCompaction(t)
			payload, _ := json.Marshal(map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "prompt": "continue", "tool_name": "Read"})
			var out bytes.Buffer
			var err error
			if event == "UserPromptSubmit" {
				err = runHookPrompt(dir, bytes.NewReader(payload), &out)
			} else {
				err = runHookTool(dir, bytes.NewReader(payload), &out)
			}
			if err != nil {
				t.Fatal(err)
			}
			var response sessionStartHookResponse
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.HookSpecificOutput.HookEventName != event || !strings.Contains(response.HookSpecificOutput.AdditionalContext, "Fix retry cancellation") {
				t.Fatalf("wrong hook response: %s", out.String())
			}
		})
	}
}

func TestCompactionRewrittenTranscriptIsUnmeasured(t *testing.T) {
	dir, workspace, transcript := prepareCompaction(t)
	before := strings.ReplaceAll(compactionFixture(t, "claude-before.jsonl", workspace), "Fix retry cancellation", "Rewrite unrelated text")
	compactionWrite(t, transcript, before+compactionFixture(t, "claude-after.jsonl", workspace))
	measureCompactionRecovery(dir, toolHookInput{SessionID: "compaction-fixture", CWD: workspace, TranscriptPath: transcript, ToolName: "functions.apply_patch", ToolUseID: "edit-after"})
	summary := usage.CompactionRecoverySummary(dir)
	if summary == nil || summary.Measured != 0 || summary.Unmeasured != 1 || summary.Pending != 0 {
		t.Fatalf("rewritten evidence was accepted: %+v", summary)
	}
}

// opencode sends a turn that is only an image to hook-prompt with an empty
// prompt so the session is stamped live again (#4573), and has no text part to
// carry what comes back. The recovery packet was marked delivered there and
// dropped; it waits for a turn that can carry it.
func TestCompactionRecoveryWaitsOutAnEmptyPrompt(t *testing.T) {
	dir, workspace, _ := prepareCompaction(t)
	payload := mustMarshal(t, map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "prompt": ""})
	var out bytes.Buffer
	if err := runHookPrompt(dir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("an empty prompt was answered: %s", out.String())
	}
	if _, ok := readLiveSessions(dir)["compaction-fixture"]; !ok {
		t.Error("an empty prompt did not stamp the session live")
	}
	if _, packet := compactionRecovery(dir, "compaction-fixture", workspace); !strings.Contains(packet, "Fix retry cancellation") {
		t.Fatalf("the recovery packet was spent on an empty prompt: %q", packet)
	}
}

func TestCompactionCapturePreservesActionableFailureReasons(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	workspace := t.TempDir()

	// 1. Missing transcript file -> transcript_missing with stage=stat_before and os=ENOENT.
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	captureCompaction(dir, precompactHookInput{
		SessionID:      "compaction-fixture",
		CWD:            workspace,
		TranscriptPath: missingPath,
	})
	events := usage.Events(dir, 1)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	gotMissing := events[0]
	if gotMissing.CompactionError != "transcript_missing" || gotMissing.CompactionStage != "stat_before" || gotMissing.CompactionOSError != "ENOENT" || gotMissing.CompactionSource == "" {
		t.Fatalf("missing transcript event = %+v", gotMissing)
	}
	if strings.Contains(gotMissing.CompactionSource, missingPath) {
		t.Fatalf("compaction_source leaked raw path: %q", gotMissing.CompactionSource)
	}
	if note := compactionFailureNote(gotMissing); !strings.Contains(note, "could not read") || !strings.Contains(note, "stage=stat_before") || !strings.Contains(note, "os=ENOENT") {
		t.Fatalf("unexpected log note for missing transcript: %q", note)
	}

	// 2. Identity mismatch -> transcript_identity_mismatch.
	mismatchPath := filepath.Join(t.TempDir(), "mismatch.jsonl")
	compactionWrite(t, mismatchPath, compactionFixture(t, "claude-before.jsonl", workspace))
	captureCompaction(dir, precompactHookInput{
		SessionID:      "wrong-session-id",
		CWD:            workspace,
		TranscriptPath: mismatchPath,
	})
	events = usage.Events(dir, 1)
	if events[0].CompactionError != "transcript_identity_mismatch" || events[0].CompactionStage == "" {
		t.Fatalf("identity mismatch event = %+v", events[0])
	}

	// 3. Consistency check failure -> transcript_changing with size/mtime/read diagnostics.
	t0 := time.Unix(1700000000, 0).UTC()
	reason, detail := classifyCompactionError(
		&sources.CompactionStageError{
			Stage: "consistency_check",
			Err: &sources.TranscriptChangingError{
				SameFile:    true,
				BeforeSize:  4096,
				AfterSize:   4608,
				BeforeMTime: t0,
				AfterMTime:  t0.Add(15 * time.Millisecond),
				ReadMS:      4,
			},
		},
		"read_file",
		"codex:12345678",
	)
	if reason != "transcript_changing" || detail.Stage != "consistency_check" || detail.BeforeSize != 4096 || detail.AfterSize != 4608 || detail.MTimeDeltaMS != 15 || detail.ReadMS != 4 {
		t.Fatalf("changing classification = %q %+v", reason, detail)
	}
	usage.RecordCompactionCapture(dir, usage.CompactionCapture{
		Key:     usage.CompactionKey{Session: "s1", Workspace: "w1"},
		Error:   reason,
		Failure: detail,
	})
	changingEvent := usage.Events(dir, 1)[0]
	if changingEvent.CompactionBeforeSize != 4096 || changingEvent.CompactionAfterSize != 4608 || changingEvent.CompactionMTimeDeltaMS != 15 || changingEvent.CompactionReadMS != 4 {
		t.Fatalf("changing event = %+v", changingEvent)
	}
	if note := compactionFailureNote(changingEvent); !strings.Contains(note, "size=4096->4608") || !strings.Contains(note, "mtime_delta=15ms") || !strings.Contains(note, "read=4ms") {
		t.Fatalf("changing log note missing diagnostics: %q", note)
	}
}

// The session-start block is three pieces from three builders: the project's
// standing decisions, the recalled sessions, and what the machine keeps
// tripping over. 439 and 440 both turned out to be a first piece deciding what
// the second one got, so this holds the composition where all three have
// something to say: every part arrives, and the whole stays small enough to
// sit at the top of a session (#2465).
func TestTheSessionStartBlockCarriesAllThreePieces(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	// The hook reads the project from where it stands, so the sessions have to
	// be recorded for this directory: the store's name is the path with its
	// separators turned into dashes, which is how every harness writes it.
	work := filepath.Join(tmp, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	// The cwd as JSON writes it: a Windows path carries backslashes, and the
	// fixture would not parse with them raw (#2808).
	workJSON, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, encodedProjectDir(work))
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	project := strings.TrimPrefix(filepath.ToSlash(work), "/")
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)

	at := func(least int) string {
		return time.Now().Add(-time.Duration(least) * time.Minute).UTC().Format(time.RFC3339)
	}
	wall := "psql: error: connection refused while starting the migration runner pool"
	var lines []string
	for k := range 4 {
		sid := fmt.Sprintf("w%d", k)
		lines = []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":`+string(workJSON)+`,"message":{"role":"user","content":"the migration runner keeps stalling on the pool"}}`, sid, at(300-10*k)),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":`+string(workJSON)+`,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"psql -h db.internal -c 'select 1'"}}]}}`, sid, at(299-10*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":`+string(workJSON)+`,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, sid, at(298-10*k), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"cwd":`+string(workJSON)+`,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services start postgresql@16"}}]}}`, sid, at(297-10*k)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":`+string(workJSON)+`,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Successfully started postgresql@16"}]}}`, sid, at(296-10*k)),
		}
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	note := `{"ts":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","project":"` + project + `","kind":"promoted",` +
		`"session":"claude:w0","state":"accepted","title":"the migration runner keeps stalling on the pool",` +
		`"text":"the retry budget stays at 5 after the pool change"}`
	if err := os.WriteFile(notes, []byte(note+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	block := hookContextBlock(t, dir)
	for what, needle := range map[string]string{
		"the standing decision": "retry budget stays at 5",
		"a recalled session":    "recalled from",
		"the wall":              "separate sessions hit",
		"the remedy":            "brew services start postgresql@16",
	} {
		if !strings.Contains(block, needle) {
			t.Errorf("%s is missing from the session start:\n%s", what, block)
		}
	}
	// Three builders, one block, and it opens a session: a page of text here is
	// paid for on every start.
	if len(block) > 8<<10 {
		t.Errorf("the block is %d bytes", len(block))
	}
}

// hookContextBlock runs the session-start hook and returns the context it
// injected.
func hookContextBlock(t *testing.T, dir string) string {
	t.Helper()
	out := captureHookStdout(t, func() {
		if err := runHookContext(dir, true); err != nil {
			t.Fatal(err)
		}
	})
	return out
}

// loggedIDsForKind reads the session ids the usage log recorded for one kind,
// through the same file `deja log` reads.
func loggedIDsForKind(t *testing.T, dir, kind string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	b, err := os.ReadFile(usage.Path(dir))
	if err != nil {
		t.Fatalf("no usage log: %v", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Kind string   `json:"kind"`
			IDs  []string `json:"ids"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != kind {
			continue
		}
		for _, id := range e.IDs {
			out[id] = true
		}
	}
	return out
}

func TestHookToolAndAfterNormalizeDialectsAndSkipScratch(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"inv-1", "inv-2", "inv-3"} {
		ts := time.Date(2026, 8, 1+i, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(proj, id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"user","content":"update pkg/billing/invoice.go"}}`, id, ts),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"u1","name":"Edit","input":{"file_path":"/work/app/pkg/billing/invoice.go","old_string":"a","new_string":"b"}},{"type":"text","text":"Decision: keep invoice rounding in pkg/billing/invoice.go deterministic."}]}}`, id, ts),
		})
	}
	wall := "(eval):1: no matches found: --include=*.py"
	now := time.Now().UTC()
	for k := range 2 {
		at := now.Add(-time.Duration(120-20*k) * time.Minute).Format(time.RFC3339)
		id := fmt.Sprintf("fx-%d", k)
		writeClaudeFixture(t, filepath.Join(proj, id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"user","content":"find python files"}}`, id, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"git grep --include=*.py pattern"}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","is_error":true,"content":%q}]}}`, id, at, wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"git grep --include='*.py' pattern"}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/work/app","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","is_error":false,"content":"match.py:10: pattern"}]}}`, id, at),
		})
	}
	if err := index.Ensure(indexDir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	for name, payload := range map[string]any{
		"mcp_fs_write scratch": map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "mcp_fs_write",
			"tool_input":      map[string]any{"AbsolutePath": "/Users/alex/.gemini/antigravity/brain/c1/task.md"},
			"session_id":      "scratch-1",
			"cwd":             "/work/app",
		},
		"Write claude memory": map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Write",
			"tool_input":      map[string]any{"file_path": "/Users/alex/.claude/projects/app/memory/notes.md"},
			"session_id":      "scratch-2",
			"cwd":             "/work/app",
		},
	} {
		var out bytes.Buffer
		if err := runHookTool(indexDir, bytes.NewReader(mustMarshal(t, payload)), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(out.String()) != "" {
			t.Errorf("%s should be skipped as scratch, got: %s", name, out.String())
		}
	}

	for name, payload := range map[string]any{
		"mcp_fs_read paths after scratch": map[string]any{
			"hook_event_name": "PostToolUse",
			"tool_name":       "mcp_fs_read",
			"tool_input": map[string]any{
				"paths": []string{
					"/Users/alex/.gemini/antigravity/brain/c1/scratch/plan.md",
					"/work/app/pkg/billing/invoice.go",
				},
			},
			"session_id": "dialect-read-paths",
			"cwd":        "/work/app",
		},
		"mcp_fs_write AbsolutePath URI": map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "mcp_fs_write",
			"tool_input":      map[string]any{"AbsolutePath": "file:///work/app/pkg/billing/invoice.go"},
			"session_id":      "dialect-write-uri",
			"cwd":             "/work/app",
		},
		"Write tool_args JSON string": map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Write",
			"tool_args":       `{"paths":["/work/app/pkg/billing/invoice.go"]}`,
			"session_id":      "dialect-tool-args",
			"cwd":             "/work/app",
		},
	} {
		var out bytes.Buffer
		if err := runHookTool(indexDir, bytes.NewReader(mustMarshal(t, payload)), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "invoice.go has been worked on in 3 sessions") {
			t.Errorf("%s did not emit file history line, got: %q", name, out.String())
		}
	}

	for name, payload := range map[string]any{
		"mcp_exec_cmd CommandLine": map[string]any{
			"hook_event_name": "PostToolUseFailure",
			"tool_name":       "mcp_exec_cmd",
			"tool_input":      map[string]any{"CommandLine": "git grep --include=*.py target"},
			"tool_response":   map[string]string{"stderr": wall},
			"session_id":      "after-mcp-exec",
			"cwd":             "/work/app",
		},
		"shell cmd": map[string]any{
			"hook_event_name": "PostToolUse",
			"tool_name":       "shell",
			"tool_input":      map[string]any{"cmd": "git grep --include=*.py target"},
			"tool_output":     wall,
			"session_id":      "after-shell-cmd",
			"cwd":             "/work/app",
		},
		"Bash tool_args": map[string]any{
			"hook_event_name": "PostToolUseFailure",
			"tool_name":       "Bash",
			"tool_args":       `{"CommandLine":"git grep --include=*.py target"}`,
			"error":           wall,
			"session_id":      "after-tool-args",
			"cwd":             "/work/app",
		},
	} {
		var out bytes.Buffer
		if err := runHookToolAfter(indexDir, bytes.NewReader(mustMarshal(t, payload)), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "git grep --include='*.py' pattern") {
			t.Errorf("%s did not emit fix pair, got: %q", name, out.String())
		}
	}
}

func TestHookAntigravityCallZeroCoalescesDigestAndPromptAndStripsFailureHeader(t *testing.T) {
	tmp := hermeticEnv(t)
	indexDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", indexDir)

	repo := filepath.Join(t.TempDir(), "work", "billing")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}

	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "billing")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range 15 {
		ts := now.Add(-time.Duration(24+i) * time.Hour).Format(time.RFC3339)
		id := fmt.Sprintf("recent-%02d", i)
		writeClaudeFixture(t, filepath.Join(proj, id+".jsonl"), id, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"routine billing deployment verification pass %d for ledger snapshots"}}`, id, ts, i),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":"verified ledger deployment snapshot %d cleanly."}}`, id, ts, i),
		})
	}
	oldTS := now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(proj, "old-zorb.jsonl"), "old-zorb", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"old-zorb","timestamp":%q,"message":{"role":"user","content":"why did billing worker stall on ZORBWAX_MTU"}}`, oldTS),
		fmt.Sprintf(`{"type":"assistant","sessionId":"old-zorb","timestamp":%q,"message":{"role":"assistant","content":"billing worker stalled because ZORBWAX_MTU was unset; exporting ZORBWAX_MTU=1420 fixed it."}}`, oldTS),
	})
	if err := index.Ensure(indexDir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	transcript := filepath.Join(tmp, "agy-transcript.jsonl")
	userLine := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nwhy did billing worker stall on ZORBWAX_MTU\n</USER_REQUEST>\n"}`
	if err := os.WriteFile(transcript, []byte(userLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := mustMarshal(t, map[string]any{
		"invocationNum":  0,
		"conversationId": "conv-dual-0",
		"transcriptPath": transcript,
		"workspacePaths": []string{repo},
	})
	var out bytes.Buffer
	if err := runHookAntigravity(indexDir, bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp antigravityHookResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("invalid JSON: %q (%v)", out.String(), err)
	}
	if len(resp.InjectSteps) != 1 {
		t.Fatalf("len(InjectSteps) = %d, want 1 coalesced step: %+v", len(resp.InjectSteps), resp.InjectSteps)
	}
	msg := resp.InjectSteps[0].EphemeralMessage
	if count := strings.Count(msg, "<deja-recall>"); count != 1 {
		t.Errorf("expected 1 <deja-recall> header after coalescing, got %d:\n%s", count, msg)
	}
	if !strings.Contains(msg, antigravityLead) {
		t.Errorf("Call-0 output missing workspace digest lead:\n%s", msg)
	}
	if !strings.Contains(msg, "ZORBWAX_MTU") {
		t.Errorf("Call-0 output missing coalesced prompt recall (ZORBWAX_MTU):\n%s", msg)
	}

	steps := []transcriptStep{
		{
			Type: "GENERIC",
			Content: "Created At: 2026-11-21T10:00:00Z\n" +
				"Completed At: 2026-11-21T10:00:01Z\n" +
				"[cmd direct | exit=1 | 12ms | 94B] pkg/billing/invoice.go:42: undefined: roundHalfEven",
		},
	}
	gotFail := latestToolFailureFromSteps(steps)
	wantFail := "pkg/billing/invoice.go:42: undefined: roundHalfEven"
	if gotFail != wantFail {
		t.Errorf("latestToolFailureFromSteps() = %q, want %q", gotFail, wantFail)
	}
}
