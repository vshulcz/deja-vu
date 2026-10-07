package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// mcpHandoff is `deja handoff` for an agent that cannot run a shell command.
// A host reached through MCP alone, with no shell to run `deja handoff` in,
// had the package that continues another session's work only if the person
// copied it in by hand. Measured over a quarter of real
// switches between harnesses, the person never ran the CLI: every handoff was an
// agent calling it through a shell, or building its own export when it had none.
//
// The subject is a session id-prefix, or a harness name for the newest session
// of that harness in this project, or nothing for the newest session here that
// is not being written right now.
func mcpHandoff(dir, name string, raw json.RawMessage) (string, int, error) {
	var a struct {
		Session string `json:"session"`
		Harness string `json:"harness"`
	}
	if err := decodeToolArgs(name, raw, &a); err != nil {
		return "", 0, err
	}
	sel := strings.TrimSpace(a.Session)
	harness := strings.TrimSpace(a.Harness)
	// "continue the claude session" names a harness, not an id.
	if harness == "" && sel != "" && !strings.ContainsAny(sel, " \t\n") && sources.IsKnownHarness(strings.ToLower(sel)) {
		harness, sel = strings.ToLower(sel), ""
	}
	if err := checkHarness(&harness); err != nil {
		return "", 0, err
	}
	if line := buildingNowForAgent(dir); line != "" {
		return frameRecall(line), 0, nil
	}
	s, why := handoffTargetForAgent(dir, sel, harness)
	if s.ID == "" {
		return "deja: " + why, 0, nil
	}
	if full, ok, err := wholeSessionForMCP(dir, s); err == nil && ok {
		s = full
	}
	text := frameRecall(handoffPrompt(digest.Handoff(s, handoffBudget)))
	return text, 1, nil
}

// handoffTargetForAgent resolves what an agent asked to continue. It never
// blocks on the index lock, the way every MCP read does, and it applies the
// MCP trust policy rather than the CLI's search one.
func handoffTargetForAgent(dir, sel, harness string) (model.Session, string) {
	if sel != "" {
		s, ok, err := index.FindByPrefix(dir, sel)
		if err != nil || !ok {
			return model.Session{}, fmt.Sprintf("no session matches %q", sel)
		}
		kept, _ := policyFilterSessionsCounted(policy.ActivationMCP, []model.Session{s})
		if len(kept) == 0 {
			return model.Session{}, fmt.Sprintf("%q is withheld by this machine's trust policy", sel)
		}
		return kept[0], ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return model.Session{}, "no working directory to find this project's sessions from"
	}
	// The caller's own session is being written right now, and the newest one
	// here is usually it. A named harness is different: the session it names
	// may have stopped a minute ago on a usage limit, inside the live window,
	// and that is exactly the one wanted.
	live := map[string]bool{}
	if harness == "" {
		live = liveSessionIDs(dir)
	}
	var newest model.Session
	for _, name := range digest.ProjectNameCandidates(cwd) {
		ss, err := index.RecentInProject(dir, name, 12)
		if err != nil {
			continue
		}
		var own []model.Session
		for _, s := range ss {
			if !index.ProjectInScopeStrict(s.Project, name) || live[s.ID] {
				continue
			}
			if harness != "" && s.Harness != harness {
				continue
			}
			own = append(own, s)
		}
		kept, _ := policyFilterSessionsCounted(policy.ActivationMCP, own)
		if len(kept) > 0 && kept[0].Updated.After(newest.Updated) {
			newest = kept[0]
		}
	}
	if newest.ID == "" {
		if harness != "" {
			return model.Session{}, fmt.Sprintf("no %s session indexed for this project — pass a session id instead", harness)
		}
		return model.Session{}, "no other session indexed for this project — pass a session id instead"
	}
	return newest, ""
}
