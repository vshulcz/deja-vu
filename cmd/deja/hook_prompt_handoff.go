package main

import (
	"io"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// handoffPromptBudget is the packet's size when the prompt hook delivers it.
// The CLI's 6 KB is for a package that is the whole first prompt; here it rides
// beside what the person typed.
const handoffPromptBudget = 4 << 10

// emitAskedHandoff answers the first prompt of a session that asks to carry on
// another session's work — "перенеси в opencode сессию claude <id>" — with that
// session's packet. On one machine 3 of 5 real switches between harnesses began
// this way, and the new agent then went looking for the session itself or the
// person built an export by hand.
//
// Only the first prompt, and only when the session is named or its harness is:
// guessed from timing and folder, a continuation fired 72 times for 2 real
// switches, because parallel work in one project is normal.
func emitAskedHandoff(dir string, input promptHookInput, asked, cwd string, plain bool, stdout io.Writer) bool {
	ask, ok := digest.HandoffIntent(asked)
	if !ok || isSpawnedReader(input.SessionID) || !firstPromptOf(dir, input.SessionID) {
		return false
	}
	self := askerLineage(dir, input.SessionID, input.TranscriptPath, input.ParentSessionID)
	src, found := askedHandoffSource(dir, ask, cwd, self)
	if !found {
		return false
	}
	key := "handoff:" + src.ID
	if alreadyInjected(dir, input.SessionID)[hookseenKey(key)] {
		return false
	}
	if full, ok, err := wholeSessionForMCP(dir, src); err == nil && ok {
		src = full
	}
	body := "deja: this prompt names " + src.Harness + " session " + digest.Short(src.ID) +
		"; its state, packaged so this session can continue it:\n\n" +
		handoffPrompt(digest.Handoff(src, handoffPromptBudget))
	rememberInjectedIDs(dir, input.SessionID, hookseenKey(key))
	usage.RecordDigestFrom(dir, usage.KindHandoff, frameRecall(body), input.SessionID, 1,
		rawSize([]model.Session{src}), nil, sessionProjects([]model.Session{src}), sessionIDs([]model.Session{src}))
	_ = emitNudgeOnly(stdout, plain, body)
	return true
}

// firstPromptOf reports whether the session has said nothing before this
// prompt. A session the index has not seen yet is new; one it holds is new
// while it has at most the one user turn being answered now.
func firstPromptOf(dir, sid string) bool {
	if strings.TrimSpace(sid) == "" {
		return false
	}
	s, ok, err := index.FindByPrefix(dir, sid)
	if err != nil {
		return false
	}
	if !ok || s.ID != sid {
		return true
	}
	turns := 0
	for _, m := range s.Messages {
		if m.Role == "user" {
			turns++
		}
	}
	return turns <= 1
}

// askedHandoffSource resolves what the prompt named: an id first, then the
// newest session of the named harness in this project.
func askedHandoffSource(dir string, ask digest.HandoffAsk, cwd string, self map[string]bool) (model.Session, bool) {
	for _, id := range ask.IDs {
		s, ok, err := index.FindByPrefix(dir, id)
		if err != nil || !ok || self[s.ID] {
			continue
		}
		if kept, _ := policyFilterSessionsCounted(policy.ActivationAuto, []model.Session{s}); len(kept) > 0 {
			return kept[0], true
		}
	}
	if ask.Harness == "" {
		return model.Session{}, false
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	var newest model.Session
	for _, name := range digest.ProjectNameCandidates(cwd) {
		ss, err := index.RecentInProject(dir, name, 12)
		if err != nil {
			continue
		}
		var own []model.Session
		for _, s := range ss {
			if s.Harness == ask.Harness && !self[s.ID] && index.ProjectInScopeStrict(s.Project, name) {
				own = append(own, s)
			}
		}
		if kept, _ := policyFilterSessionsCounted(policy.ActivationAuto, own); len(kept) > 0 && kept[0].Updated.After(newest.Updated) {
			newest = kept[0]
		}
	}
	return newest, newest.ID != ""
}
