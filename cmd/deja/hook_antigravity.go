package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Antigravity's PreInvocation hook runs before every model call and injects
// whatever steps the hook prints. That is one call per turn, not per session,
// so the digest goes in on the first invocation only — invocationNum tells us
// which one we are on.
type antigravityHookInput struct {
	InvocationNum  int      `json:"invocationNum"`
	WorkspacePaths []string `json:"workspacePaths"`
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
	// invocationNum is 1-based; anything past the first turn already has the
	// digest in its transcript. A payload deja could not read leaves it at 0,
	// which reads as the first turn — so bounding the read without this would
	// turn "blocks once per turn" into "injects the whole digest before every
	// model call" (#846).
	if !decoded || input.InvocationNum > 1 {
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
	// The payload, and nothing written back into the environment: deja used to
	// export the workspace here, which carried this call's project into the
	// next one in the same process and decided nothing else (#2185).
	digest, sessions, raw, _, _ := cachedHookDigestFor(dir, workspace)
	if digest == "" {
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	digest = frameRecall(antigravityLead + digest)
	usage.RecordDigestPolicy(dir, usage.KindHook, digest, sessions, raw,
		policy.Load().Describe(policy.ActivationAuto))
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
