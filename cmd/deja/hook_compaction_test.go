package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func compactionFixture(t *testing.T, name, workspace string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "synthetic", "compaction", name))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(workspace)
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
	payload, _ := json.Marshal(map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript})
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
	payload, _ := json.Marshal(map[string]any{"session_id": "compaction-fixture", "cwd": workspace, "source": "compact", "deja_once": true})
	withHookStdin(t, string(payload))
	out := captureStdout(t, func() {
		if err := runHookContextMode(dir, true, true); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Fix retry cancellation", "integration fixture", "two attempts", "go test", "untrusted reference data"} {
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
	_, err = io.WriteString(f, compactionFixture(t, "claude-after.jsonl", workspace))
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("append fixture: %v %v", err, closeErr)
	}
	toolPayload, _ := json.Marshal(map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript, "tool_name": "Edit", "tool_use_id": "edit-after"})
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
	payload, _ := json.Marshal(map[string]string{"session_id": "compaction-fixture", "cwd": workspace, "transcript_path": transcript})
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
