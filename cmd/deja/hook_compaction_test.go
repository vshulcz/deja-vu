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
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

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
