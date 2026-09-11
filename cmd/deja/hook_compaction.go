package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// This is one delivery per compaction, shared by the existing hook surfaces.
// The budget includes the untrusted-history frame and trailing newline.
const compactionRecoveryBytes = 4 << 10

func compactionWorkspace(cwd string) string {
	path, err := filepath.Abs(hookCWD(cwd))
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return filepath.Clean(path)
}

func compactionUsageKey(s index.CompactionState) usage.CompactionKey {
	return usage.CompactionKey{
		Session: shortHash(s.SessionID), Workspace: shortHash(s.Workspace),
		Revision: strconv.FormatUint(s.Revision, 10) + ":" + s.CapturedAt.UTC().Format(time.RFC3339Nano),
	}
}

// captureCompaction reads only the transcript named by the host. It never
// guesses another session or waits for a history rebuild on the hook path.
func captureCompaction(dir string, input precompactHookInput) {
	if input.SessionID == "" {
		return
	}
	workspace := compactionWorkspace(hookProjectPath(input.CWD, input.WorkspaceRoots))
	blockOldPacket := func() {
		rememberInjectedIDs(dir, compactionFailureKey(input.SessionID), compactionUnavailableToken(workspace))
	}
	pol := policy.Load()
	if recallIsOff() || workspace == "" || !pol.Allows(policy.ActivationAuto, workspace) || pol.Ignored(input.TranscriptPath, workspace) {
		blockOldPacket()
		return
	}
	failure := func(reason string) {
		// Precompact cleared delivery marks before this attempt. Preserve a
		// failure mark in that same ledger so an older successful packet cannot
		// masquerade as the state of this compaction, even if the index is busy.
		blockOldPacket()
		usage.RecordCompactionCapture(dir, usage.CompactionCapture{
			Key: usage.CompactionKey{Session: shortHash(input.SessionID), Workspace: shortHash(workspace)}, Error: reason,
		})
	}
	if input.TranscriptPath == "" {
		failure("missing_transcript")
		return
	}
	now := time.Now().UTC()
	transcript, err := sources.ReadCompactionTranscript(input.TranscriptPath, input.SessionID)
	if err != nil {
		failure("transcript_unavailable")
		return
	}
	if transcript.Workspace == "" || compactionWorkspace(transcript.Workspace) != workspace {
		failure("workspace_mismatch")
		return
	}
	data := digest.ExtractCompactionContext(transcript.Session, digest.ExtractOptions{})
	withCommandOutcomes(&data, transcript.Session)
	data.Truncated = data.Truncated || transcript.Truncated
	data.Freshness = compactionFreshness(workspace)
	saved, err := index.PutCompaction(dir, index.CompactionState{
		SessionID: input.SessionID, Harness: transcript.Harness, Workspace: workspace,
		Project: transcript.Session.Project, TranscriptPath: transcript.Path,
		SourceDigest: transcript.Fingerprint, SourceHeaderFingerprint: transcript.HeaderFingerprint,
		SourceBoundaryFingerprint: transcript.BoundaryFingerprint,
		SourceSize:                transcript.SourceSize, SourceMTime: transcript.SourceMTime,
		LastToolCallID: transcript.LastToolID, CapturedAt: now, Data: data,
	})
	if err != nil {
		failure("storage_unavailable")
		return
	}
	if saved.CapturedAt.Equal(now) {
		capture := usage.CompactionCapture{Key: compactionUsageKey(saved), ToolCalls: len(transcript.ToolCalls)}
		if !transcript.MetricComplete {
			capture.Error = "incomplete_transcript"
		}
		usage.RecordCompactionCapture(dir, capture)
	}
}

// withCommandOutcomes says how each recorded command went.
//
// The packet's own rule reads an exit marker out of the command text, which is
// what codex and opencode append and Claude does not: on a transcript where the
// suite failed and then passed, both commands arrived as "[recorded]", so a
// resuming agent could not tell whether the work was green when it was
// interrupted — which is the first thing it needs to know.
//
// The output is already in the transcript, one record after the command. Whether
// that output is a failure is a question the store answers in one place
// (index.FrictionLine, the same rule behind `deja friction` and the fix pairs),
// and digest cannot import index — so the pairing happens here, where both are
// in reach, rather than as a second copy of the recogniser (the drift #3473 was
// about).
//
// Newest wins: a command that failed and was then re-run green keeps the green
// outcome, because the packet carries one entry per command and the state at
// capture is what a resuming agent is owed.
func withCommandOutcomes(data *model.CompactionContext, s model.Session) {
	if len(data.Tests) == 0 {
		return
	}
	outcome := map[string]string{}
	pending := ""
	for _, m := range s.Messages {
		switch m.Role {
		case sources.RoleCommand:
			pending = compactionCommandKey(m.Text)
		case sources.RoleToolOutput:
			if pending == "" {
				continue
			}
			if _, friction := index.FrictionLine(firstFrictionLine(m.Text)); friction {
				outcome[pending] = "failed"
			} else {
				outcome[pending] = "passed"
			}
			pending = ""
		}
	}
	for i := range data.Tests {
		if got, ok := outcome[compactionCommandKey(data.Tests[i].Command)]; ok && data.Tests[i].Outcome == "recorded" {
			data.Tests[i].Outcome = got
		}
	}
}

// compactionCommandKey matches a command in the packet to the same command in the
// transcript: the packet's copy is trimmed and may carry the prompt marker a
// harness stored it with.
func compactionCommandKey(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimPrefix(cmd, "$ ")
	if i := strings.Index(cmd, "  → exit "); i > 0 {
		cmd = cmd[:i]
	}
	return strings.Join(strings.Fields(cmd), " ")
}

// firstFrictionLine is the line of a command's output that names a failure, if
// any: a failing run says so on one line and prints many.
func firstFrictionLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, friction := index.FrictionLine(line); friction {
			return line
		}
	}
	return ""
}

func compactionRecovery(dir, sessionID, cwd string) (index.CompactionState, string) {
	if recallIsOff() || sessionID == "" {
		return index.CompactionState{}, ""
	}
	state, found, err := index.Compaction(dir, sessionID, compactionWorkspace(cwd))
	if err != nil || !found {
		return index.CompactionState{}, ""
	}
	pol := policy.Load()
	if !pol.Allows(policy.ActivationAuto, state.Project) || pol.Ignored(state.TranscriptPath, state.Workspace) {
		return index.CompactionState{}, ""
	}
	ledger := loadSeen(dir)
	if ledger.injected(sessionID)[compactionDeliveryToken(state)] ||
		ledger.injected(compactionFailureKey(sessionID))[compactionUnavailableToken(state.Workspace)] {
		return index.CompactionState{}, ""
	}
	current := compactionFreshness(state.Workspace)
	captured := state.Data.Freshness
	switch {
	case current.Error != "":
		state.Data.Freshness.Error = current.Error
	case captured.Error != "" || captured.WorktreeState == "":
		state.Data.Freshness.Error = "Capture could not verify repository state. Revalidate conclusions and relevant tests."
	case current.Head != captured.Head || current.Branch != captured.Branch || current.WorktreeState != captured.WorktreeState:
		state.Data.Freshness.Error = "Repository changed since compaction. Revalidate conclusions and rerun relevant tests."
	}
	packet := digest.RenderCompactionContext(state.Data, compactionRecoveryBytes-recallFrameOverhead-1)
	return state, frameRecall(packet)
}

func compactionDeliveryToken(s index.CompactionState) string {
	key := compactionUsageKey(s)
	return "compaction:" + shortHash(key.Workspace+":"+key.Revision)
}

func compactionUnavailableToken(workspace string) string {
	return "compaction-unavailable:" + shortHash(workspace)
}

func compactionFailureKey(sessionID string) string {
	return onceDigestKey("compaction:" + sessionID)
}

// emitCompactionRecovery marks a packet delivered only after the write
// succeeds, so a closed output pipe does not consume the next recovery.
func emitCompactionRecovery(dir, sessionID, cwd, event string, shape hookToolShape, stdout io.Writer) (bool, error) {
	state, packet := compactionRecovery(dir, sessionID, cwd)
	if packet == "" {
		return false, nil
	}
	var out []byte
	switch shape {
	case hookToolPlain:
		out = []byte(packet)
	case hookToolCrush:
		out, _ = json.Marshal(struct {
			Version int    `json:"version"`
			Context string `json:"context"`
		}{1, packet})
	default:
		var response sessionStartHookResponse
		response.HookSpecificOutput.HookEventName = event
		response.HookSpecificOutput.AdditionalContext = packet
		out, _ = json.Marshal(response)
	}
	out = append(out, '\n')
	n, err := stdout.Write(out)
	if err == nil && n != len(out) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return true, err
	}
	rememberInjectedIDsFor(dir, sessionID, state.Project, []string{compactionDeliveryToken(state)})
	usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, packet, sessionID, 1, state.SourceSize,
		policy.Load().Describe(policy.ActivationAuto), []string{state.SessionID}, []string{state.Project})
	return true, nil
}

// The metric counts raw transcript tool invocations, including tools whose
// hooks deja does not subscribe to. Missing or rewritten evidence is recorded
// as unmeasured; hook invocations are never substituted for action counts.
func measureCompactionRecovery(dir string, input toolHookInput) {
	if !sources.IsCompactionEditTool(input.ToolName) || input.SessionID == "" {
		return
	}
	state, found, err := index.Compaction(dir, input.SessionID, compactionWorkspace(hookProjectPath(input.CWD, input.WorkspaceRoots)))
	if err != nil || !found {
		return
	}
	pol := policy.Load()
	if !pol.Allows(policy.ActivationAuto, state.Project) || pol.Ignored(state.TranscriptPath, state.Workspace) {
		return
	}
	key := compactionUsageKey(state)
	if _, pending := usage.PendingCompaction(dir, key); !pending {
		return
	}
	result := usage.CompactionRecovery{Key: key}
	path := input.TranscriptPath
	if path == "" {
		path = state.TranscriptPath
	}
	current, err := sources.ReadCompactionTranscript(path, input.SessionID)
	if err != nil {
		result.Error = "transcript_unavailable"
	} else if current.Workspace == "" || compactionWorkspace(current.Workspace) != state.Workspace ||
		current.HeaderFingerprint != state.SourceHeaderFingerprint ||
		!sources.MatchesCompactionBoundary(current, state.SourceSize, state.SourceBoundaryFingerprint) {
		result.Error = "transcript_rewritten"
	} else if count, ok := sources.CountActionsBeforeEdit(current, state.SourceSize, state.LastToolCallID, input.ToolUseID); ok {
		result.ActionsBeforeEdit = count
	} else {
		result.Error = "boundary_unavailable"
	}
	usage.RecordCompactionRecovery(dir, result)
}
