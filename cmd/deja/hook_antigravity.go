package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/policy"
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
	// Antigravity runs the hook with the working directory set to the folder
	// holding hooks.json, not the user's project, so scoping recall by cwd
	// would silently recall nothing. The payload names the real workspace.
	workspace := ""
	if len(input.WorkspacePaths) > 0 {
		workspace = input.WorkspacePaths[0]
	}
	if workspace == "" {
		workspace = workspaceFromConversation(input.TranscriptPath, input.ConversationID)
	}
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
		block := antigravityFixPair(dir, latestToolFailure(input.TranscriptPath),
			input.ConversationID, workspace)
		if block == "" {
			block = antigravityPromptBlock(dir, latestUserRequest(input.TranscriptPath),
				input.ConversationID, workspace)
		}
		if block == "" {
			fmt.Fprintln(stdout, "{}")
			return nil
		}
		b, err := json.Marshal(antigravityHookResponse{
			InjectSteps: []antigravityInjectStep{{EphemeralMessage: block}},
		})
		if err != nil {
			fmt.Fprintln(stdout, "{}")
			return nil
		}
		fmt.Fprintln(stdout, string(b))
		return nil
	}
	// The payload, and nothing written back into the environment: deja used to
	// export the workspace here, which carried this call's project into the
	// next one in the same process and decided nothing else (#2185).
	digest, sessions, raw, _, _, ids, projects := cachedHookDigestFor(dir, workspace)
	if digest == "" {
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	digest = frameRecall(startLead(antigravityLead) + digest)
	rememberDigestInjected(dir, input.ConversationID)
	usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, digest, "", sessions, raw,
		policy.Load().Describe(policy.ActivationAuto), ids, projects)
	b, err := json.Marshal(antigravityHookResponse{
		InjectSteps: []antigravityInjectStep{{EphemeralMessage: digest}},
	})
	if err != nil {
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

const antigravityLead = "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call recall_context with a term from it to pull the full details before acting. If recalled history genuinely helps the task, tell the user in one short line what deja-vu recalled and how you reused it; otherwise do not mention it.\n"
