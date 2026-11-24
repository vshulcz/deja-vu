package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Antigravity's PreInvocation hook runs before every model call and injects
// whatever steps the hook prints. It is the only place a hook can speak here:
// PreToolUse fires but its reason never reaches the model, and PostToolUse must
// answer with an empty object (checked on antigravity-cli 1.1.13). So this one
// event carries both channels — the digest on the first invocation, and the
// question's own answer on the ones after it.
type antigravityHookInput struct {
	InvocationNum  int      `json:"invocationNum"`
	WorkspacePaths []string `json:"workspacePaths"`
	// The transcript is where the question lives: antigravity has no
	// per-prompt event, so the newest user turn is read from here.
	TranscriptPath string `json:"transcriptPath"`
	ConversationID string `json:"conversationId"`
}

type antigravityInjectStep struct {
	EphemeralMessage string `json:"ephemeralMessage,omitempty"`
}

type antigravityHookResponse struct {
	InjectSteps []antigravityInjectStep `json:"injectSteps,omitempty"`
}

func runHookAntigravity(dir string, stdin io.Reader, stdout io.Writer) error {
	var input antigravityHookInput
	// Bounded, like the other hooks: an unbounded decode waited for the host to
	// close the pipe, which cost 20 s per turn on a host that holds it (#846).
	payload := readHookPayload(stdin, hookStdinWait)
	decoded := json.Unmarshal(payload, &input) == nil && len(payload) > 0
	// A payload deja could not read leaves invocationNum at 0, which reads as
	// the first call — so bounding the read without this would turn "once per
	// turn" into "before every model call" (#846).
	if !decoded {
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	// The conversation is being written: its MCP recall must not answer with
	// it. The plugin's Stop hook drops the stamp when the turn ends.
	if !recallIsOff() {
		markSessionLive(dir, input.ConversationID)
	}
	// Antigravity runs the hook with the working directory set to the folder
	// holding hooks.json, not the user's project, so scoping recall by cwd
	// would silently recall nothing. The payload names the real workspace.
	// Decode the transcript tail once per invocation rather than re-reading and
	// re-unmarshaling up to 128 KB of JSONL four times across checkpoint,
	// failure, prompt, and edit checks.
	tailSteps := transcriptTailSteps(input.TranscriptPath)
	convFallback := ""
	if len(input.WorkspacePaths) == 0 {
		convFallback = workspaceFromConversation(input.TranscriptPath, input.ConversationID)
	}
	workspace, projectWorkspace := selectAntigravityWorkspaces(input.WorkspacePaths, convFallback, tailSteps)
	if !isSubagentTranscriptPath(input.TranscriptPath) {
		syncAntigravitySessionRules(dir, projectWorkspace, input.ConversationID, tailSteps)
	}
	coordRecordAntigravitySteps(dir, projectWorkspace, input.ConversationID, tailSteps)
	// A compaction throws away the blocks this conversation was shown while the
	// record of having sent them outlives them, so the memory it just lost is
	// the memory recall refuses to send again.
	forgetOnCheckpoint(dir, input.ConversationID, newestCheckpointFromSteps(tailSteps))
	// The compaction itself, caught up from the transcript the way Gemini's is:
	// the steps before the CHECKPOINT are still in the file, so the session is
	// captured as it stood then and the packet rides this invocation.
	compactionWS := projectWorkspace
	if compactionWS == "" {
		compactionWS = workspace
	}
	if compactionWS == "" {
		compactionWS = sources.Home()
	}
	recovery := antigravityCompactionPacket(dir, input, compactionWS)
	// Past the first invocation the digest is already in the transcript, and
	// the harness has no per-prompt event of its own — so this is where the
	// question gets answered. Silence is the usual result, and the prompt
	// path's dedupe keeps an answer to one per question rather than one per
	// model call.
	//
	// The count starts at zero and restarts on every turn (measured on
	// antigravity-cli 1.1.13: one two-turn conversation ran 0..21 and then 0
	// again). Reading it as 1-based put the digest in twice per turn, and
	// reading it as per-conversation put it in again on every turn of a
	// continued one — so the conversation's own ledger decides, and the
	// counter only says which call inside the turn we are on.
	if input.InvocationNum > 0 || digestAlreadyInjected(dir, input.ConversationID) {
		// A command that just failed is the more urgent memory, and it is only
		// reachable from here: PostToolUse is handed the error and its contract
		// allows no answer at all.
		failedOut := latestToolFailureFromSteps(tailSteps)
		if failedOut == "" && latestToolStepSucceededFromSteps(tailSteps) {
			coordResetFailStreak(dir, input.ConversationID, projectWorkspace)
		}
		block := antigravityFixPair(dir, failedOut,
			input.ConversationID, projectWorkspace)
		if block == "" {
			block = antigravityPromptBlockFrom(dir, latestUserRequestFromSteps(tailSteps),
				input.ConversationID, projectWorkspace, input.TranscriptPath, nil)
		}
		// The line the pre-tool hook gives before an edit, for the file the
		// last call edited: PreToolUse cannot say it here, so it rides the
		// next invocation with the edit's result.
		var steps []antigravityInjectStep
		if recovery != "" {
			steps = append(steps, antigravityInjectStep{EphemeralMessage: recovery})
		}
		editCalls := latestEditCallsFromSteps(tailSteps)
		for _, c := range editCalls {
			if sources.IsCompactionEditTool(c.ToolName) {
				measureCompactionRecovery(dir, toolHookInput{
					SessionID:      input.ConversationID,
					TranscriptPath: input.TranscriptPath,
					CWD:            compactionWS,
					ToolName:       c.ToolName,
				})
				break
			}
		}
		if line := editFileCallLines(dir, editCalls, input.ConversationID, projectWorkspace); line != "" {
			steps = append(steps, antigravityInjectStep{EphemeralMessage: line})
		}
		if failedOut == "" {
			if cmdLine := commandCallLines(dir, latestCommandCallsFromSteps(tailSteps), input.ConversationID, projectWorkspace); cmdLine != "" {
				steps = append(steps, antigravityInjectStep{EphemeralMessage: cmdLine})
			}
		}
		if planLine := antigravityPlanBlock(dir, tailSteps, input.ConversationID, projectWorkspace); planLine != "" {
			steps = append(steps, antigravityInjectStep{EphemeralMessage: planLine})
		}
		if block != "" {
			steps = append(steps, antigravityInjectStep{EphemeralMessage: block})
		}
		steps = coalesceAntigravitySteps(steps)
		if len(steps) == 0 {
			fmt.Fprintln(stdout, "{}")
			return nil
		}
		b, err := json.Marshal(antigravityHookResponse{InjectSteps: steps})
		if err != nil {
			fmt.Fprintln(stdout, "{}")
			return nil //nolint:nilerr // a hook never fails the host
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	// The payload, and nothing written back into the environment: deja used to
	// export the workspace here, which carried this call's project into the
	// next one in the same process and decided nothing else (#2185).
	var steps []antigravityInjectStep
	if recovery != "" {
		steps = append(steps, antigravityInjectStep{EphemeralMessage: recovery})
	}
	var self []string
	if input.ConversationID != "" {
		lineage, _ := askerLineageCutForHarness(dir, input.ConversationID, input.TranscriptPath, "antigravity")
		for id := range lineage {
			self = append(self, id)
		}
		self = append(self, antigravitySubagentIDs(input.TranscriptPath)...)
		sort.Strings(self)
	}
	digest, sessions, raw, _, _, ids, projects := cachedHookDigestFor(dir, projectWorkspace, self...)
	digest, _, projects = attachClaudeAutoMemory(dir, input.ConversationID, input.TranscriptPath, "antigravity", hookCWD(projectWorkspace), digest, projects)
	rememberDigestInjected(dir, input.ConversationID)
	if digest != "" {
		if len(ids) > 0 {
			rememberInjectedIDs(dir, input.ConversationID, ids...)
		}
		digest = frameRecall(startLead(antigravityLead) + digest)
		usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, digest, "", sessions, raw,
			policy.Load().Describe(policy.ActivationAuto), ids, projects)
		steps = append(steps, antigravityInjectStep{EphemeralMessage: digest})
	}
	failedOut := latestToolFailureFromSteps(tailSteps)
	if failedOut == "" && latestToolStepSucceededFromSteps(tailSteps) {
		coordResetFailStreak(dir, input.ConversationID, projectWorkspace)
	}
	block := antigravityFixPair(dir, failedOut, input.ConversationID, projectWorkspace)
	if block == "" {
		block = antigravityPromptBlockFrom(dir, latestUserRequestFromSteps(tailSteps),
			input.ConversationID, projectWorkspace, input.TranscriptPath, ids)
	}
	if planLine := antigravityPlanBlock(dir, tailSteps, input.ConversationID, projectWorkspace); planLine != "" {
		steps = append(steps, antigravityInjectStep{EphemeralMessage: planLine})
	}
	if block != "" {
		steps = append(steps, antigravityInjectStep{EphemeralMessage: block})
	}
	steps = coalesceAntigravitySteps(steps)
	if len(steps) == 0 {
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	b, err := json.Marshal(antigravityHookResponse{InjectSteps: steps})
	if err != nil {
		fmt.Fprintln(stdout, "{}")
		return nil //nolint:nilerr // a hook never fails the host
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

func isSingleRecallFrame(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	_, body, ok := cutFrameHeader(trimmed)
	if !ok {
		return false
	}
	_, after, ok := strings.Cut(body, recallFrameFooter)
	return ok && strings.TrimSpace(after) == ""
}

func appendUnwrappedRecallParts(dst []string, msg string, seen map[string]bool) []string {
	rest := strings.TrimSpace(msg)
	for rest != "" {
		_, body, ok := cutFrameHeader(rest)
		if !ok {
			if !seen[rest] {
				seen[rest] = true
				dst = append(dst, rest)
			}
			break
		}
		if before, after, ok := strings.Cut(body, recallFrameFooter); ok {
			if part := strings.TrimSpace(before); part != "" && !seen[part] {
				seen[part] = true
				dst = append(dst, part)
			}
			rest = strings.TrimSpace(after)
			continue
		}
		if part := strings.TrimSpace(body); part != "" && !seen[part] {
			seen[part] = true
			dst = append(dst, part)
		}
		break
	}
	return dst
}

// coalesceAntigravitySteps merges multiple hook messages (recovery packet,
// edit/command file lines, fix pairs, digest, and prompt recall) into a single
// framed EphemeralMessage so trust headers appear once per invocation.
func coalesceAntigravitySteps(steps []antigravityInjectStep) []antigravityInjectStep {
	var nonEmpty []antigravityInjectStep
	for _, s := range steps {
		if strings.TrimSpace(s.EphemeralMessage) != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}
	if len(nonEmpty) == 1 && (isSingleRecallFrame(nonEmpty[0].EphemeralMessage) || !strings.Contains(nonEmpty[0].EphemeralMessage, "<deja-recall>")) {
		return nonEmpty
	}
	seen := make(map[string]bool, len(nonEmpty))
	var parts []string
	for _, s := range nonEmpty {
		parts = appendUnwrappedRecallParts(parts, s.EphemeralMessage, seen)
	}
	if len(parts) == 0 {
		return nil
	}
	return []antigravityInjectStep{{EphemeralMessage: frameRecall(strings.Join(parts, "\n\n"))}}
}

// antigravityCompactionPacket captures a compaction the transcript shows and
// hands back its recovery packet once, or "".
func antigravityCompactionPacket(dir string, input antigravityHookInput, workspace string) string {
	if input.ConversationID == "" || input.TranscriptPath == "" || workspace == "" {
		return ""
	}
	pre := precompactHookInput{SessionID: input.ConversationID, TranscriptPath: input.TranscriptPath, CWD: workspace}
	seenKey := onceDigestKey("compaction-seen:" + input.ConversationID)
	seenMap := loadSeen(dir).injected(seenKey)
	catchUpCompactionWith(dir, pre, func() (sources.CompactionTranscript, bool, error) {
		return sources.ReadAntigravityCompactionIfNew(input.TranscriptPath, input.ConversationID, workspace, func(fp string) bool {
			return seenMap["compaction-seen:"+shortHash(fp)]
		})
	})
	var out bytes.Buffer
	if delivered, _ := emitCompactionRecovery(dir, input.ConversationID, workspace, "PreInvocation", hookToolPlain, &out); !delivered {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func antigravitySubagentIDs(transcriptPath string) []string {
	if transcriptPath == "" {
		return nil
	}
	fi, err := os.Stat(transcriptPath)
	if err != nil || fi.Size() > 16<<20 {
		return nil
	}
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return nil
	}
	var set = map[string]bool{}
	s := string(data)
	for {
		_, afterKey, ok := strings.Cut(s, "conversationId")
		if !ok {
			break
		}
		_, afterQ1, ok := strings.Cut(afterKey, "\"")
		if !ok {
			break
		}
		id, tail, ok := strings.Cut(afterQ1, "\"")
		if !ok {
			break
		}
		if len(id) >= 30 {
			set[id] = true
		}
		s = tail
	}
	var out []string
	for id := range set {
		out = append(out, id)
	}
	return out
}

const antigravityLead = "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call the deja tool in \"context\" mode (or recall_context) with the session id printed beside it — an id is exact, a phrase is a guess — to pull the full details before acting. \nIf one of these helps, open your reply with that one line and nothing more about it: déjà vu: <what it said> — reusing it (deja:<session id>). If none helps, say nothing about them.\n"
