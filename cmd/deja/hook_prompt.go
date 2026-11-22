package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/redact"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
//
// 1024 cut the two quoted lines mid-sentence, and what it cut was the tail —
// the paths, names and numbers an answer reuses. In real-world stores, by
// replaying the sweep and comparing each block against what the agent actually
// answered next: blocks carrying words the answer went on to use rose from 20%
// to 25%, and blocks carrying a conclusion from 55 to 57 of 100, for 24 tokens
// a message. 2048 buys nothing further (25% again, 253 tokens).
const promptHookBudget = 1536

// dejaVuMaxMessages caps how large a session can be and still read as one
// rememberable episode. Marathon catch-all sessions rank into everything.
const dejaVuMaxMessages = 300

type promptHookInput struct {
	Prompt    hookPromptText `json:"prompt"`
	SessionID string         `json:"session_id"`
	// Cursor names the conversation conversation_id and sends no session_id;
	// read as no session at all, the cooldown was never recorded and the same
	// block went out on every prompt of the conversation (#3287).
	ConversationID string `json:"conversation_id"`
	// ParentSessionID is the session that spawned this one, from a host that
	// knows: the opencode plugin asks opencode for a task session's parent.
	// That session is live and asking through this one (#4548).
	ParentSessionID string `json:"parent_session_id"`
	// TranscriptPath is the asker's transcript, read for what a session the
	// index does not hold yet was forked from (#4549).
	TranscriptPath string `json:"transcript_path"`
	// CWD is what the harness says the project is. Reading only the
	// environment meant a host that sends the payload without exporting
	// CLAUDE_PROJECT_DIR recalled nothing (#759).
	CWD string `json:"cwd"`
	// Cursor leaves cwd empty and names the project here instead.
	WorkspaceRoots []string `json:"workspace_roots"`
	// HookEventName is BeforeAgent from Gemini CLI, whose compactions are
	// found in the transcript at the next prompt.
	HookEventName string          `json:"hook_event_name"`
	Harness       string          `json:"harness"`
	Messages      json.RawMessage `json:"messages"`
	SkipIDs       []string        `json:"skip_ids"`
	// Grok spells all of this in camelCase. See hook_grok.go.
	grokEnvelope
}

// adopt fills in what grok spells differently, so this prompt's recall is filed
// under the session that asked for it.
func (i *promptHookInput) adopt() {
	i.SessionID, i.TranscriptPath, i.WorkspaceRoots = i.adoptCommon(i.SessionID, i.ConversationID, i.TranscriptPath, i.WorkspaceRoots)
}

// hookPromptText reads a prompt that arrives either as a string (Claude Code,
// Cursor) or as content parts (Kimi sends [{"type":"text","text":"…"}]). A
// harness whose shape we do not read looks exactly like one where recall never
// matches, so both are accepted rather than assumed.
type hookPromptText string

func (h *hookPromptText) UnmarshalJSON(b []byte) error { //nolint:unparam // json.Unmarshaler fixes the signature
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*h = hookPromptText(withoutInjectedContext(s))
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &parts); err != nil {
		return nil //nolint:nilerr // an unknown shape means no prompt, not a broken hook
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	*h = hookPromptText(withoutInjectedContext(sb.String()))
	return nil
}

// withoutInjectedContext drops context an earlier hook already put in front of
// the question. Gemini hands BeforeAgent the whole request, which by then
// carries the <hook_context> block SessionStart returned — deja's own recall.
// Left in, the six search terms all come from that block and none from the
// user, so every prompt recalls whatever the last one did. Gemini escapes the
// angle brackets of anything inside, so both spellings are removed.
func withoutInjectedContext(s string) string {
	for _, tag := range []struct{ open, close string }{
		{"<hook_context>", "</hook_context>"},
		{"&lt;hook_context&gt;", "&lt;/hook_context&gt;"},
		{"<deja-recall>", "</deja-recall>"},
		{"&lt;deja-recall&gt;", "&lt;/deja-recall&gt;"},
	} {
		for {
			start := strings.Index(s, tag.open)
			if start < 0 {
				break
			}
			end := strings.Index(s[start:], tag.close)
			if end < 0 {
				// An unclosed block is still not the user's words, and what
				// follows it cannot be told apart from more of the same.
				s = s[:start]
				break
			}
			s = s[:start] + s[start+end+len(tag.close):]
		}
	}
	return strings.TrimSpace(s)
}

// plain=true prints the bare digest for hosts that inject a hook's stdout
// verbatim — Kimi does that and reads no JSON field for context.
func runHookPromptMode(dir string, stdin io.Reader, stdout io.Writer, plain bool) error {
	var input promptHookInput
	// Bounded, like the session-start hooks: a host that opens stdin and holds
	// it without finishing the payload used to stall this hook until the
	// harness killed it — on every user message (#846). Still decoded rather
	// than unmarshalled, so a host that writes anything after the object (a
	// second line, a trailing NUL) keeps its recall instead of losing the
	// whole payload to a syntax error.
	payload := readHookPayload(stdin, hookStdinWait)
	// The error is kept, not swallowed: a decode that fails on a later field —
	// `{"prompt":"…","session_id":123}` — reads the prompt, injects on it, and
	// leaves a row saying the host named no session, when it named one deja
	// could not read (#2773). The prompt is what this hook recalls from, so a
	// payload it cannot read at all injects nothing and this never fires.
	unreadable := json.NewDecoder(bytes.NewReader(payload)).Decode(&input) != nil
	input.adopt()
	adoptCopilotHost(payload)
	adoptCursorHost(payload)
	// The kill switch. It reached the session-start hook and nothing else, so
	// this hook — every user message, and the wiring for seven harnesses —
	// kept injecting on a machine with recall off (#2701).
	//
	// Ahead of the nudge too, which is a wider reading than #2701 needed: the
	// nudge is built from the prompt in front of it rather than from the
	// index, so nothing recalled leaks through it. It is still deja speaking
	// unasked into a session that asked for none of it.
	if recallIsOff() {
		return nil
	}
	shape := hookToolClaude
	if plain {
		shape = hookToolPlain
	} else if copilotHookOutput {
		shape = hookToolCopilot
	}
	// Written down for the surfaces that are told no session id: this hook drops
	// the caller's own session by hand below, and the MCP tool cannot (#3945).
	markSessionLive(dir, input.SessionID)
	setActiveDejaDir(dir)
	// A turn that is only an image reaches here with no prompt so the stamp
	// above is renewed (#4573). opencode has no text part to put an answer
	// in, so the compaction packet waits for a turn or tool call that can
	// carry it rather than being marked delivered and dropped.
	// Grok discards what this hook returns, so the answer waits for the
	// session's next tool hook (hook_deferred.go) rather than being logged as
	// arrived to a session that never saw it (#4588).
	if strings.TrimSpace(string(input.Prompt)) == "" {
		return nil
	}
	if input.HookEventName == "BeforeAgent" {
		catchUpCompaction(dir, precompactHookInput{SessionID: input.SessionID, TranscriptPath: input.TranscriptPath, CWD: input.CWD, WorkspaceRoots: input.WorkspaceRoots})
	}
	catchUpZCodeCompaction(dir, precompactHookInput{SessionID: input.SessionID, TranscriptPath: input.TranscriptPath, CWD: input.CWD, WorkspaceRoots: input.WorkspaceRoots})
	if delivered, err := emitCompactionRecovery(dir, input.SessionID, hookProjectPath(input.CWD, input.WorkspaceRoots), "UserPromptSubmit", shape, stdout); delivered {
		return err
	}
	// The failure the user just reported is worth capturing whether or not
	// this prompt also earns a recall, so it is decided before the gates that
	// silence the recall path.
	// The envelope comes off before anything is decided: a person's question
	// with the host's reminder appended is still the question, and the
	// reminder's own words are not what to search for (#3156).
	//
	// A host that quoted the whole prompt, as `opencode run "<question>"` does,
	// sent the question all the same.
	asked := prompt.Unwrap(digest.StripHarnessBlocks(string(input.Prompt)))
	// The payload first, then the export, then where the process stands: the
	// export is set once per process and will not change, so reading it alone
	// answered a second payload with the first one's project (#2182).
	cwd := hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots))
	prevAssistant := syncLiveSessionRules(dir, cwd, input, asked)
	seenFile := loadSeen(dir)
	seen := seenFile.injected(input.SessionID)
	advisories := collectPromptDirectAdvisories(dir, cwd, input.SessionID, asked, isSpawnedReader(input.SessionID), seen, nil, prevAssistant)
	failNudge := failureNudge(dir, asked)
	// Nobody asked. A harness delivers its own plumbing as the next user turn —
	// a finished background task, a system reminder, a slash command's envelope
	// — and the hook fires on it like a question. The terms are then the
	// envelope's own field names plus an id that exists nowhere else, and what
	// they match is another notification in another session: a block of noise
	// injected as recalled history, under a line telling the user deja fires on
	// noise (#3156). Same test the digest uses on transcript messages.
	if asked == "" || digest.IsAgentArtifact(asked) {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, nil, false, plain, stdout)
	}
	terms := prompt.Terms(focusPromptForTaskRecall(asked))
	if !promptTermsWorthAsking(terms) {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, nil, false, plain, stdout)
	}
	// Version, not just presence: terms hash into buckets an index from
	// another format never wrote, so a stale store answers every prompt with
	// nothing and looks exactly like a user with no history (#777).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.HookDamaged(dir) {
		if !index.RebuildInProgress(dir) {
			requestWarmup(dir)
		}
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, nil, false, plain, stdout)
	}
	// A new session whose first words ask to carry on another one gets that
	// session's packet instead of a recall on the words of the request.
	if emitAskedHandoff(dir, input, asked, cwd, plain, stdout) {
		return nil
	}
	// The same question, from the same reader, was answered a moment ago. The
	// cooldowns below are per session shown, so an identical prompt arriving
	// every minute — a `/loop 1m …` sends the same text on each tick — walked
	// one session further down the ranking each time: 25 blocks from 21
	// different sessions in one evening, none of them the loop's subject
	// (#3189). "You have been here" is about the question, not the tick.
	//
	// Not for a spawned agent: a fleet is many readers behind one key, and ten
	// agents sent out on the same work each need the answer once rather than
	// the first one taking it for all of them (#2534).
	if !isSpawnedReader(input.SessionID) &&
		seenFile.asked(input.SessionID, askKey(terms), repeatAskWindow) {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, nil, false, plain, stdout)
	}
	// Rank THIS project's sessions by how well they match the prompt terms
	// (IDF-weighted), rather than reconstructing an AND query — natural
	// prompts are full of filler that poisons an AND.
	// The sessions this agent session has already been shown, and the one it is
	// writing, are discarded a few lines below on their id alone. Handing them
	// to the ranking means they are never read: on a real store that is 15 of
	// 26 candidates, each read from disk in full before being dropped.
	// What this agent session was already shown, by the text of the block
	// rather than by the session it came from. Banning the session banned
	// everything else it holds: In real-world stores, walking one working
	// session of eighty messages, the second half got three blocks where the
	// same messages without the ban got thirty-eight, and only seven of those
	// were word-for-word repeats. The same past session answers many questions
	// in a day, and after the first answer it went silent.
	//
	// A session shown a moment ago is skipped before it is read — that is what
	// keeps an answering call from reading every candidate off disk. A session
	// shown an hour ago is fair game again: the same past work answers many
	// questions in a day, and what stops it repeating itself is the block
	// fingerprint below, not a ban on where it came from.
	recent := seenFile.recent(input.SessionID, injectionCooldown)
	// The same cooldown across agent sessions in this project. Without it the
	// window reset every time a new agent session opened, which is how one
	// marathon reached 110 servings (#2038).
	//
	// The names are asked for once: each ask runs `git worktree list`, and in a
	// repository with hundreds of worktrees that is tens of milliseconds.
	names := cachedProjectNameCandidates(cwd)
	projectKey := ""
	if len(names) > 0 {
		projectKey = names[0]
	}
	// Except for a spawned agent. The cooldown above counts servings across
	// agent sessions, which is right for a reader working through a run of
	// messages — #2038 measured 92% repeats and one marathon served 110 times.
	// A fleet is the other shape: ten agents spawned together are ten readers,
	// each seeing a session once, and the answer they all need is the one the
	// cooldown has just spent. Measured: of three agents spawned on the same
	// work, the first was given it and the other two were sent out with
	// nothing (#2534). Their own per-reader key and the block fingerprint still
	// stop the same agent being handed the same thing twice.
	var inProject map[string]bool
	if !isSpawnedReader(input.SessionID) {
		inProject = seenFile.recentInProject(projectKey, injectionCooldown)
	}
	skip := promptHookSkipSet(recent, inProject)
	for _, id := range input.SkipIDs {
		if id != "" {
			skip[id] = true
		}
	}
	// The asker and what counts as it: a spawn's parent, a sub-agent's parent,
	// the sub-agents it spawned (#4548), a fork's source (#4549).
	self, cut := askerLineageCutForHarness(dir, input.SessionID, input.TranscriptPath, input.Harness, input.ParentSessionID)
	for id := range self {
		skip[id] = true
	}
	sel := selectPromptRankedCandidates(dir, cwd, asked, names, terms, self, skip, cut, true)
	ranked, matched, strong, idfOf := sel.ranked, sel.matched, sel.strong, sel.idfOf
	if len(ranked) == 0 {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, skip, false, plain, stdout)
	}
	ss, confident, worthDigest, extraMatches := sel.ss, sel.confident, sel.worthDigest, sel.extraMatches
	if len(ss) == 0 {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, ranked, matched, strong, skip, true, plain, stdout)
	}
	ss = hydratePromptSessionsForDigest(dir, ss, cut)
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not, so the
	// concluded_session arm scored a behaviour the product never had.
	if len(names) > 0 || !projectMatchesAnyTerm(ss[0].Project, terms) {
		ss = search.LeadWithConclusion(ss, terms)
	}
	for i := range ss {
		index.MarkUserRules(dir, &ss[i], names)
	}
	// A rejected session is not an equal answer, and the mark has to travel
	// with it into the block the agent reads (#761).
	ss, rejectedWarning := orderForInjection(ss)

	// "You have been here" on the strength of one word teaches the user to
	// ignore the line. The recall itself still goes in.
	// Not on a host that shows it to nobody: the window it keeps is spent.
	// The payload is sized to the claim. Two informative terms is a real match
	// and earns the digest; a single rare term is a hint, and a hint that costs
	// a full digest on every message is most of what deja spends. The pointer
	// keeps it discoverable — the agent learns there is history here and can
	// ask for it — for 134 bytes against the 0.5-1.3 KB a real digest measures.
	nudge := joinPromptAdvisories(joinAdvisoryItems(advisories), failNudge)
	// A long session stops paying for full digests once it has had a few. On
	// this machine's injection log the fifth and later per-prompt blocks of a
	// session were acted on 0 times in 53, against 1 in 12 for the second to
	// fourth, and they were 29% of every per-prompt byte. Past the budget the
	// block is the one-line pointer, which still says history is here. A
	// question asked before keeps the full block: that claim is the strong one.
	//
	// A spawned agent gets the pointer from the start. Its block is appended to
	// instructions the parent already wrote with the context it had, and on
	// this machine's transcripts 582 such blocks in a month were followed by a
	// recall 0 times and by a file action the instructions did not already name
	// 5 times: 18% of every token deja injected, at 53k tokens an action.
	localWorth := effectiveDigestWorth(worthDigest, seen, input.SessionID, ss, terms)
	body, shown, fullDigest, built := buildPromptBlockCandidate(dir, cwd, input.SessionID, asked, nudge, rejectedWarning, ss, ranked, matched, strong, idfOf, terms, confident, localWorth, seen, skip, extraMatches)
	if !built {
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, ranked, matched, strong, skip, true, plain, stdout)
	}
	releaseSeenLock, _ := holdHookseenLock(dir)
	latestFile := loadSeen(dir)
	if !isSpawnedReader(input.SessionID) && latestFile.asked(input.SessionID, askKey(terms), repeatAskWindow) {
		releaseSeenLock()
		return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, advisories, failNudge, nil, nil, nil, skip, false, plain, stdout)
	}
	latestSeen := latestFile.injected(input.SessionID)
	keptAdv, freshAdvToks := filterFreshPromptAdvisories(advisories, latestSeen)
	latestNudge := joinPromptAdvisories(joinAdvisoryItems(keptAdv), failNudge)
	latestWorth := effectiveDigestWorth(worthDigest, latestSeen, input.SessionID, ss, terms)
	if len(keptAdv) != len(advisories) || latestWorth != localWorth {
		body, shown, fullDigest, built = buildPromptBlockCandidate(dir, cwd, input.SessionID, asked, latestNudge, rejectedWarning, ss, ranked, matched, strong, idfOf, terms, confident, latestWorth, latestSeen, skip, extraMatches)
		if !built {
			releaseSeenLock()
			return emitCommittedNudgeOnly(dir, cwd, input.SessionID, asked, keptAdv, failNudge, ranked, matched, strong, skip, true, plain, stdout)
		}
	}
	// The same block twice is wallpaper, and the reader stops looking. Judged
	// on what would be shown, so a session whose other lines answer the next
	// question still gets to answer it.
	fp := blockFingerprint(body)
	if latestSeen[fp] || latestSeen[digestSeenPrefix+fp] {
		if input.SessionID != "" && len(freshAdvToks) > 0 {
			rememberInjectedIDs(dir, input.SessionID, freshAdvToks...)
		}
		releaseSeenLock()
		return emitNudgeOnly(stdout, plain, latestNudge)
	}
	if fullDigest {
		fp = digestSeenPrefix + fp
	}
	// And the same sentence twice under another session's id is the same
	// wallpaper: the fingerprint above covers the block, whose header carries
	// the id and the date, so a fork or a subagent transcript of the same work
	// passes it every time. In real-world stores, over eight near-identical
	// nudges: nine sessions cited, fifteen quoted lines, eleven of them
	// distinct, and the worst line four times under four ids.
	filtered, freshQuotes, anyFresh := withoutSeenQuotes(latestSeen, body)
	switch {
	case anyFresh:
		body = filtered
	case !strings.HasPrefix(input.SessionID, spawnReaderPrefix):
		// Every quote was delivered to this reader already, so the block is the
		// same injection wearing another session's header.
		if input.SessionID != "" && len(freshAdvToks) > 0 {
			rememberInjectedIDs(dir, input.SessionID, freshAdvToks...)
		}
		releaseSeenLock()
		return emitNudgeOnly(stdout, plain, latestNudge)
	}
	// A spawned agent is a new reader even when the parent has heard it all: the
	// key it recalls under is the parent plus the instructions, which exists to
	// stop a re-spawn of the same agent repeating itself — not to send the
	// second one in blind. Handing it what the parent saw is the whole point.
	if input.SessionID != "" && len(freshAdvToks) > 0 {
		rememberInjectedIDs(dir, input.SessionID, freshAdvToks...)
	}
	rememberInjectedIDs(dir, input.SessionID, fp)
	rememberInjectedIDs(dir, input.SessionID, freshQuotes...)
	// Stamped, because the question is answered again once the window passes,
	// where a block fingerprint holds for the life of the session.
	rememberInjectedIDsFor(dir, input.SessionID, "", []string{askKey(terms)})
	rememberInjectedFor(dir, input.SessionID, projectKey, shown)
	showLine := confident && notesHaveReader() && dejaVuLineDue(dir, input.SessionID)
	releaseSeenLock()
	out := frameRecall(body)
	return emitPromptBlock(stdout, dir, input.SessionID, out, shown, terms, unreadable, plain, showLine)
}

func effectiveDigestWorth(worthDigest bool, seen map[string]bool, sessionID string, ss []model.Session, terms []string) bool {
	if !worthDigest {
		return false
	}
	if !isSpawnedReader(sessionID) && !digestBudgetSpent(seen, sessionID) {
		return true
	}
	return search.AskedBefore(ss[0], terms) != ""
}

func buildPromptBlockCandidate(
	dir, cwd, sessionID, asked, nudge, rejectedWarning string,
	ss, ranked []model.Session,
	matched, strong []int,
	idfOf map[string]float64,
	terms []string,
	confident, worthDigest bool,
	seen, skip map[string]bool,
	extraMatches int,
) (string, []model.Session, bool, bool) {
	if worthDigest {
		full, shownDigest, ok := promptDigestBodyWithOverflowShown(dir, cwd, sessionID, ss, confident, terms, idfOf, asked, nudge, rejectedWarning, ranked, matched, strong, skip, extraMatches)
		switch {
		case ok:
			shown := ss
			if len(shownDigest) > 0 {
				shown = shownDigest
			}
			return full, shown, true, true
		case !confident:
			return buildWeakPromptPointerBody(dir, cwd, asked, ss, terms, nudge, ranked, matched, strong, seen, skip), ss, false, true
		default:
			return "", nil, false, false
		}
	}
	return buildWeakPromptPointerBody(dir, cwd, asked, ss, terms, nudge, ranked, matched, strong, seen, skip), ss, false, true
}

type promptRankSelection struct {
	ranked          []model.Session
	matched, strong []int
	idfOf           map[string]float64
	ss              []model.Session
	confident       bool
	worthDigest     bool
	extraMatches    int
}

// hydratePromptSessionsForDigest reloads non-marathon candidate sessions with
// edit, wrote, and command records intact so trajectory-verified conclusions
// can be selected from real indexed stores (defaultServableRoles strips them).
func hydratePromptSessionsForDigest(dir string, ss []model.Session, cut map[string]time.Time) []model.Session {
	if len(ss) == 0 {
		return ss
	}
	roles := map[string]bool{
		"user":                 true,
		"assistant":            true,
		"tool":                 true,
		sources.RoleToolOutput: true,
		sources.RoleEdit:       true,
		sources.RoleWrote:      true,
		sources.RoleCommand:    true,
	}
	var ids []index.Identity
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		for _, m := range s.Messages {
			if m.Role != "" {
				roles[m.Role] = true
			}
		}
		if s.Harness == "" || s.ID == "" || len(s.Messages) > dejaVuMaxMessages || s.Held > dejaVuMaxMessages {
			continue
		}
		id := index.Identity{Harness: s.Harness, ID: s.ID}
		if !seen[id.Key()] {
			seen[id.Key()] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ss
	}
	full, err := index.FindManyByIdentityInRoles(dir, ids, roles)
	if err != nil || len(full) == 0 {
		return ss
	}
	byKey := make(map[string]model.Session, len(full))
	for _, f := range full {
		if len(cut) > 0 {
			f = cutSessionAt(f, cut)
		}
		if len(f.Messages) > 0 {
			byKey[f.Key()] = f
		}
	}
	out := slices.Clone(ss)
	for i := range out {
		if f, ok := byKey[out[i].Key()]; ok && len(f.Messages) >= len(out[i].Messages) {
			out[i].Messages = f.Messages
		}
	}
	return out
}

func selectPromptRankedCandidates(
	dir, cwd, asked string,
	names, terms []string,
	self, skip map[string]bool,
	cut map[string]time.Time,
	includeUnscopedNamed bool,
) promptRankSelection {
	promptStart := time.Now()
	const promptSoftBudget = 1500 * time.Millisecond
	ranked, matched, strong, naming, idfOf, err := index.ProjectRelevantNamingCut(dir, names, terms, prompt.Candidates, skip, cut)
	// The rule every other surface applies: a promoted note goes in front of
	// the transcript it was distilled from. This hook ranks sessions and never
	// builds a search.Hit, so it had no note-over-source rule at all and the
	// two slots below could go to the transcript while its own note waited
	// behind it (#2803).
	search.LiftNoteSessionsAligned(ranked, matched, strong, naming)
	rankedAlreadyShown.Store(0)
	for _, s := range ranked {
		if skip[s.ID] {
			rankedAlreadyShown.Add(1)
		}
	}
	if err != nil || len(ranked) == 0 {
		return promptRankSelection{ranked: ranked, matched: matched, strong: strong, idfOf: idfOf}
	}
	leadTerms := terms
	ordered := byIdentifying(terms, idfOf)
	if len(ordered) > 0 {
		leadTerms = ordered[:min(len(ordered), leadTermsKept)]
	}
	pk := &search.PromptPicker{
		Pol:              policy.Load(),
		Terms:            terms,
		OrderedTerms:     ordered,
		Asked:            asked,
		IDFOf:            idfOf,
		TopIDF:           index.TopIDF(dir),
		Self:             self,
		SelfProjectWords: selfProjectWordSet(cwd),
		LeadTerms:        leadTerms,
		Unscoped:         len(names) == 0,
		CwdPlain:         sources.PlainProjectName(cwd),
		Deadline:         promptStart.Add(promptSoftBudget),
		MaxMessages:      dejaVuMaxMessages,
		Focus:            focusSession,
		DigestBudget:     digestBudget,
		ProjectMatches:   projectMatchesAnyTerm,
	}
	pk.Pick(ranked, matched, strong, naming)
	// One slot left and duplicates took the window: ask for a wider one. Skip
	// the wider retry when the soft latency budget is already exhausted, and
	// exclude the candidates already examined in the first pass (#5901).
	if len(pk.Picked()) < 2 && pk.CrowdedOut() > 0 && time.Since(promptStart) < promptSoftBudget {
		widerSkip := make(map[string]bool, len(skip)+len(ranked))
		for id := range skip {
			widerSkip[id] = true
		}
		for _, s := range ranked {
			widerSkip[s.ID] = true
		}
		wider, wmatched, wstrong, wnaming, _, werr := index.ProjectRelevantNamingCut(
			dir, names, terms, prompt.Candidates*widerWindow, widerSkip, cut)
		if werr == nil {
			pk.Pick(wider, wmatched, wstrong, wnaming)
		}
	}
	if len(pk.Picked()) < 2 && time.Since(promptStart) < promptSoftBudget {
		if left, right, ok := search.WhenClausePairTerms(pk.Asked, terms, idfOf); ok {
			if other := pk.UncoveredWhenClause(left, right); len(other) > 0 {
				clauseSkip := search.SkipWithPicked(skip, pk.Picked())
				if oRanked, oMatched, oStrong, oNaming, oIDF, oErr := index.ProjectRelevantNamingCut(
					dir, names, other, prompt.Candidates, clauseSkip, cut); oErr == nil {
					pk.AddIDF(oIDF)
					pk.Pick(oRanked, oMatched, oStrong, oNaming)
				}
			}
		}
	}
	if len(pk.Picked()) == 1 && pk.HasScopedTarget() && time.Since(promptStart) < promptSoftBudget {
		scopedNames := pk.ScopeFilterNames(names)
		for _, subj := range pk.SpokenSubjectTerms(2.5) {
			if len(pk.Picked()) >= 2 || time.Since(promptStart) >= promptSoftBudget {
				break
			}
			subjSkip := search.SkipWithPicked(skip, pk.Picked())
			if sRanked, sMatched, sStrong, sNaming, sIDF, sErr := index.ProjectRelevantNamingCut(
				dir, scopedNames, []string{subj}, prompt.Candidates, subjSkip, cut); sErr == nil {
				pk.AddIDF(sIDF)
				pk.Pick(sRanked, sMatched, sStrong, sNaming)
			}
		}
	}
	if includeUnscopedNamed {
		pickUnscopedNamedProjects(dir, pk, names, terms, skip, cut, promptStart, promptSoftBudget)
	}
	return promptRankSelection{
		ranked:       ranked,
		matched:      matched,
		strong:       strong,
		idfOf:        idfOf,
		ss:           search.OrderDistinctDateUpdates(pk.TrimWeakThirdSession(), terms, pk.IDFOf),
		confident:    pk.Confident(),
		worthDigest:  pk.WorthDigest(),
		extraMatches: pk.ExtraMatches(),
	}
}

func cleanTargetPathToken(raw string) string {
	p := strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(p, "defined in "); ok {
		p = strings.TrimSpace(after)
	}
	if idx := strings.Index(p, " ("); idx > 0 {
		p = strings.TrimSpace(p[:idx])
	}
	if idx := strings.IndexByte(p, ':'); idx > 0 {
		p = strings.TrimSpace(p[:idx])
	}
	if p == "" || strings.ContainsAny(p, " \t\n") || (!strings.Contains(p, "/") && !looksLikePromptFileRef(p)) {
		return ""
	}
	return p
}

func extractLeadTargetPaths(line string) []string {
	seg, _, _ := strings.Cut(line, " | ")
	head, _, _ := strings.Cut(seg, " — ")
	isDefined := false
	for _, pfx := range []string{
		"likely targets: ",
		"likely target: ",
		"files edited in related sessions: ",
		"files edited in related session: ",
		"files edited there: ",
		"defined in ",
	} {
		if after, ok := strings.CutPrefix(head, pfx); ok {
			head = after
			isDefined = pfx == "defined in "
			break
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		if p := cleanTargetPathToken(raw); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if isDefined {
		add(head)
		if _, alsoRest, ok := strings.Cut(head, " (also "); ok {
			add(strings.TrimSuffix(alsoRest, ")"))
		}
		return out
	}
	if colon := strings.Index(head, ": "); colon >= 0 && !strings.Contains(head[:colon], "/") && !looksLikePromptFileRef(strings.TrimSpace(head[:colon])) {
		head = head[colon+2:]
	}
	for part := range strings.SplitSeq(head, ",") {
		add(part)
	}
	return out
}

func extractAllGuidancePaths(text string) map[string]bool {
	paths := map[string]bool{}
	for rawLine := range strings.SplitSeq(text, "\n") {
		for adv := range strings.SplitSeq(rawLine, " | ") {
			adv = strings.TrimSpace(adv)
			if adv == "" || strings.HasPrefix(adv, standingLabel) || strings.HasPrefix(adv, "in .deja/memory.jsonl") {
				continue
			}
			for _, p := range extractLeadTargetPaths(adv) {
				paths[strings.ToLower(p)] = true
			}
			if _, rest, ok := strings.Cut(adv, " — "); ok {
				for sfx := range strings.SplitSeq(rest, " — ") {
					sfx = strings.TrimSpace(sfx)
					if strings.HasPrefix(sfx, "callers of ") {
						if _, after, ok := strings.Cut(sfx, ": "); ok {
							for part := range strings.SplitSeq(after, ",") {
								if p := cleanTargetPathToken(part); p != "" {
									paths[strings.ToLower(p)] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return paths
}

func lineAddsNewTargetPath(existingNudge, candidateLine string) bool {
	if candidateLine == "" {
		return false
	}
	candPaths := extractLeadTargetPaths(candidateLine)
	if len(candPaths) == 0 {
		return !nudgeHasLocalizedGuidance(existingNudge)
	}
	existing := extractAllGuidancePaths(existingNudge)
	for _, p := range candPaths {
		if !existing[strings.ToLower(p)] {
			return true
		}
	}
	return false
}

func nudgeHasLocalizedGuidance(nudge string) bool {
	if strings.Contains(nudge, "likely targets:") ||
		strings.Contains(nudge, "likely target:") ||
		strings.Contains(nudge, "defined in ") {
		return true
	}
	return len(extractAllGuidancePaths(nudge)) > 0
}

func textHasLiveGuardrail(s string) bool {
	for _, m := range []string{standingLabel, repoDecisionLabel, decisionLabel, modelOrderLabel, revertedLabel, breakageLabel} {
		if m != "" && strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func textHasLiveTestRecipe(s string) bool {
	low := strings.ToLower(s)
	for _, m := range []string{"go test", "pytest", "cargo test", "npm test", "test target:", "verified"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

func compactTargetSupplementLine(existing, candidate string) string {
	return compactTargetSupplementLineWithPrefix(existing, candidate, "files edited there: ", 3)
}

func compactTargetSupplementLineWithPrefix(existing, candidate, prefix string, maxPaths int) string {
	if candidate == "" {
		return ""
	}
	if !nudgeHasLocalizedGuidance(existing) {
		return candidate
	}
	existingPaths := extractAllGuidancePaths(existing)
	existingLow := strings.ToLower(existing)
	var newPaths []string
	seen := map[string]bool{}
	for _, p := range extractLeadTargetPaths(candidate) {
		lowP := strings.ToLower(p)
		if p == "" || seen[lowP] || existingPaths[lowP] || strings.Contains(existingLow, lowP) {
			continue
		}
		seen[lowP] = true
		newPaths = append(newPaths, p)
		if maxPaths > 0 && len(newPaths) >= maxPaths {
			break
		}
	}
	if len(newPaths) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(strings.Join(newPaths, ", "))
	parts := strings.Split(candidate, " — ")
	if len(parts) > 1 {
		needGuard := !textHasLiveGuardrail(existing)
		needTest := !textHasLiveTestRecipe(existing)
		for _, seg := range parts[1:] {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			tagged := " — " + seg
			if needGuard && textHasLiveGuardrail(tagged) {
				out.WriteString(tagged)
				needGuard = false
				continue
			}
			if needTest && textHasLiveTestRecipe(tagged) {
				out.WriteString(tagged)
				needTest = false
			}
		}
	}
	return out.String()
}

func attachRankedConsensusFallback(
	dir, cwd, sessionID, asked, nudge string,
	ranked []model.Session,
	matched, strong []int,
	seen, skip map[string]bool,
	dryRun bool,
) string {
	rcLine, rcTok := rankedTouchedConsensusLineForPrompt(dir, cwd, asked, ranked, matched, strong, seen, skip)
	if rcLine == "" || !lineAddsNewTargetPath(nudge, rcLine) {
		return nudge
	}
	compact := compactTargetSupplementLine(nudge, rcLine)
	if compact == "" {
		return nudge
	}
	if !dryRun {
		rememberInjectedIDs(dir, sessionID, rcTok)
	}
	return joinPromptAdvisories(nudge, compact)
}

func buildWeakPromptPointerBody(
	dir, cwd, asked string,
	ss []model.Session,
	terms []string,
	nudge string,
	ranked []model.Session,
	matched, strong []int,
	seen, skip map[string]bool,
) string {
	ptr := weakRecallPointerAskedWithNudgeIn(dir, cwd, asked, ss, terms, nudge, skip)
	if !strings.Contains(ptr, "files edited ") {
		if rcLine, _ := rankedTouchedConsensusLineForPrompt(dir, cwd, asked, ranked, matched, strong, seen, skip); rcLine != "" && lineAddsNewTargetPath(nudge, rcLine) {
			if compact := compactTargetSupplementLine(nudge, rcLine); compact != "" {
				ptr += compact + "\n"
			}
		}
	}
	body := ptr + shownInjectionWarning(ss, ss[:1])
	if nudge != "" {
		body += "\n" + nudge
	}
	return body
}

// promptHookSkipSet is the sessions shown a moment ago, to this reader or in
// this project, which the ranking leaves out unread.
func promptHookSkipSet(recent, inProject map[string]bool) map[string]bool {
	skip := make(map[string]bool, len(recent)+len(inProject)+1)
	for id := range recent {
		skip[id] = true
	}
	for id := range inProject {
		skip[id] = true
	}
	return skip
}

// emitPromptBlock logs the block and writes it out in the shape the host reads.
func emitPromptBlock(stdout io.Writer, dir, sessionID, out string, ss []model.Session, terms []string, unreadable, plain, showLine bool) error {
	if unreadable {
		usage.RecordDigestFromUnread(dir, usage.KindDejaVu, out, sessionID, len(ss), rawSize(ss),
			terms, sessionProjects(ss), sessionIDs(ss))
	} else {
		usage.RecordDigestFrom(dir, usage.KindDejaVu, out, sessionID, len(ss), rawSize(ss),
			terms, sessionProjects(ss), sessionIDs(ss))
	}
	if plain {
		fmt.Fprintln(stdout, out)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	resp.HookSpecificOutput.AdditionalContext = out
	if showLine {
		resp.SystemMessage = dejaVuLine(ss[0], viaTerms(out, terms)...)
	}
	// No presentable topic leaves SystemMessage empty, and the context goes in
	// silently rather than flashing harness plumbing at the user.
	writePromptResponse(stdout, resp)
	return nil
}

// promptDigestBody builds the full block for a prompt that earned a digest. It
// reports false when the digest came out empty or quotes nothing that holds
// the question.
func promptDigestBody(dir, sessionID string, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string) (string, bool) {
	return promptDigestBodyIn(dir, "", sessionID, ss, confident, terms, idfOf, asked, nudge, rejectedWarning, nil, nil, nil, nil)
}

func promptDigestBodyIn(dir, cwd, sessionID string, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string, ranked []model.Session, matched, strong []int, skip map[string]bool) (string, bool) {
	return promptDigestBodyWithOverflow(dir, cwd, sessionID, ss, confident, terms, idfOf, asked, nudge, rejectedWarning, ranked, matched, strong, skip, 0)
}

func promptDigestBodyWithOverflow(dir, cwd, sessionID string, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string, ranked []model.Session, matched, strong []int, skip map[string]bool, extraMatches int) (string, bool) {
	body, _, ok := promptDigestBodyWithOverflowShown(dir, cwd, sessionID, ss, confident, terms, idfOf, asked, nudge, rejectedWarning, ranked, matched, strong, skip, extraMatches)
	return body, ok
}

func promptDigestBodyWithOverflowShown(dir, cwd, sessionID string, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string, ranked []model.Session, matched, strong []int, skip map[string]bool, extraMatches int) (string, []model.Session, bool) {
	return promptDigestBodyWithLeadAndOverflowShown(dir, cwd, sessionID, !blockAlreadySentThisSession(dir, sessionID), ss, confident, terms, idfOf, asked, nudge, rejectedWarning, ranked, matched, strong, skip, extraMatches)
}

func promptDigestBodyWithLead(dir, cwd, sessionID string, includeLead bool, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string, ranked []model.Session, matched, strong []int, skip map[string]bool) (string, bool) {
	body, _, ok := promptDigestBodyWithLeadAndOverflowShown(dir, cwd, sessionID, includeLead, ss, confident, terms, idfOf, asked, nudge, rejectedWarning, ranked, matched, strong, skip, 0)
	return body, ok
}

func digestCarriesPromptSubject(dir, digestText string, shown []model.Session, cwdProjects, terms []string, idfOf map[string]float64) bool {
	named := namedLeadTerms(terms, idfOf, index.TopIDF(dir))
	shownInCallerScope := len(shown) > 0 && len(cwdProjects) > 0 && slices.Contains(cwdProjects, shown[0].Project)
	shownMatchesNamedProject := len(shown) > 0 && projectMatchesAnyTerm(shown[0].Project, terms)
	if shownMatchesNamedProject && shownInCallerScope {
		shownMatchesNamedProject = false
	}
	soleSubj := []string(nil)
	if shownInCallerScope && len(search.NonGenericRareTerms(terms, idfOf, 0)) == 1 {
		soleSubj = search.NonGenericRareTerms(terms, idfOf, max(mediumSalientIDFFloor, index.TopIDF(dir)-0.9))
	}
	switch {
	case shownMatchesNamedProject:
		return search.QuoteCarriesTerms(digestText, terms, 1)
	case len(soleSubj) == 1:
		return search.QuoteCarriesTerms(digestText, soleSubj, 1) || search.QuoteCarriesTerms(digestText, terms, min(2, len(terms)))
	case len(named) == 1:
		return search.QuoteCarriesTerms(digestText, named, 1) || search.QuoteCarriesTerms(digestText, terms, min(2, len(terms)))
	default:
		return search.QuoteCarriesTerms(digestText, terms, min(2, len(terms)))
	}
}

func promptDigestBodyWithLeadAndOverflowShown(dir, cwd, sessionID string, includeLead bool, ss []model.Session, confident bool, terms []string, idfOf map[string]float64, asked, nudge, rejectedWarning string, ranked []model.Session, matched, strong []int, skip map[string]bool, extraMatches int) (string, []model.Session, bool) {
	hadDistinct := len(ss) >= 2 && search.HasDistinctDate(ss)
	cwdProjects := cachedProjectNameCandidates(cwd)
	lead := ""
	if includeLead {
		lead = promptHookLead
	}
	// Hand the digest the query's words most-identifying first. It has no
	// way of its own to tell "mm_status" from "decide", and the session
	// that answers often says both — measured live, ten of the answers
	// this hook newly returns open on the ordinary word.
	budget := digestBudget(confident || hadDistinct)
	fullBudget := budget
	if !includeLead {
		budget = min(budget, max(190, 320-min(len(nudge), 130)))
	} else if nudge != "" {
		budget = max(280, budget-len(lead)/2-min(len(nudge), 420)-130)
	}
	digestText, shown := search.AutoRecallDigestShowing(ss, budget, byIdentifying(terms, idfOf), asked)
	if strings.TrimSpace(digestText) == "" {
		return "", nil, false
	}
	// The sessions passed on where their words were said, but the block is
	// what the agent reads. When no line it quotes holds two of the question's
	// words — unless the question named a single rare subject and the quote
	// holds that subject — each line is about something else that shares one
	// word with it. When the question joins two rare subjects, the block must
	// also cover them rather than answering a two-subject question with a
	// session that only ever heard of one.
	if !digestCarriesPromptSubject(dir, digestText, shown, cwdProjects, terms, idfOf) {
		if budget < fullBudget {
			fullText, fullShown := search.AutoRecallDigestShowing(ss, fullBudget, byIdentifying(terms, idfOf), asked)
			if strings.TrimSpace(fullText) != "" && digestCarriesPromptSubject(dir, fullText, fullShown, cwdProjects, terms, idfOf) {
				digestText, shown = fullText, fullShown
			} else {
				return "", nil, false
			}
		} else {
			return "", nil, false
		}
	}
	// The line the agent is asked to say names a session; it has to be one
	// the digest shows. The top of the ranking is skipped when it has
	// nothing quotable, and the opener then credited a session that was
	// not in the block.
	cite := ss[0]
	actionSources := ss
	if len(shown) > 0 {
		cite = shown[0]
		actionSources = shown
		rejectedWarning = shownInjectionWarning(ss, shown)
	}
	actionLine := ""
	al := sessionActionMapLineForPrompt(dir, cwd, asked, actionSources, skip)
	if al == "" && len(ranked) > 0 && !nudgeHasLocalizedGuidance(nudge) {
		al, _ = rankedTouchedConsensusLineForPrompt(dir, cwd, asked, ranked, matched, strong, nil, skip)
	}
	if al != "" && lineAddsNewTargetPath(nudge, al) {
		if compact := compactTargetSupplementLine(nudge, al); compact != "" {
			if !strings.HasSuffix(digestText, "\n") {
				digestText += "\n"
			}
			actionLine = compact + "\n"
		}
	}
	unshown := 0
	if len(shown) > 0 && len(ss) > len(shown) {
		unshown = len(ss) - len(shown)
	}
	if overflow := extraMatches + unshown; overflow > 0 {
		if !strings.HasSuffix(digestText, "\n") {
			digestText += "\n"
		}
		actionLine += fmt.Sprintf("+%d more matching sessions — run deja recall to check all\n", overflow)
	}
	tail := openerLine(cite, byIdentifying(terms, idfOf))
	if nudge != "" {
		tail += "\n" + nudge
	}
	// After the first block of a session the two explanatory sentences are
	// words the agent has already read, and the third repeats the line this
	// block ends with. In real-world stores, a session receives a median
	// of 23 per-prompt blocks, the lead is 301 bytes of a 1439-byte block,
	// so 22 of those blocks carried 6.6 KB of the same sentences.
	//
	// The one line that survived the first cut does not survive this one. It
	// was kept as a caution against taking a wording match for an answer, and
	// measured against exactly that: a later block whose history is about a
	// sibling subject and carries a value nothing else holds, quoted as the
	// answer with no hedge. On the local 9B, 40 such cases: false trust 12/40
	// with the caution and 10/40 without it. It is not buying the care it was
	// there for, and it is 78 bytes on every block after the first.
	//
	// The untrusted-data frame is a different matter and stays on every
	// block: dropping its sentence doubled how often a directive planted in
	// the recalled text was obeyed (8/48 against 19/48).
	_ = sessionID
	// A repeat of the question itself is a different claim than a session
	// about the subject, and a stronger one: the agent does not have to
	// decide whether the history is relevant, only whether the answer
	// still holds. Measured over this machine's own sessions, 6.5% of
	// substantial questions are asked again in a later session, and the
	// exact-match counter in `deja stats` sees a fifth of them.
	if !search.HasDistinctDate(shown) && extraMatches == 0 && unshown == 0 {
		if again := search.AskedBefore(cite, terms); again != "" {
			if includeLead {
				lead = repeatLead(cite, again)
			}
			tail = repeatOpener(cite, again)
			if nudge != "" {
				tail += "\n" + nudge
			}
		}
	}
	return lead + rejectedWarning + digestText + actionLine + tail, shown, true
}

func projectMatchesAnyTerm(project string, terms []string) bool {
	if project == "" {
		return false
	}
	for _, t := range terms {
		if len(t) >= 4 && (index.ProjectInScope(project, t) || index.ProjectEndsWithSegment(project, t)) {
			return true
		}
	}
	return false
}

func pickUnscopedNamedProjects(dir string, pk *search.PromptPicker, names, terms []string, skip map[string]bool, cut map[string]time.Time, start time.Time, budget time.Duration) {
	if len(names) > 0 || len(pk.Picked()) >= 2 || len(terms) < 2 || time.Since(start) >= budget {
		return
	}
	var projTerms []string
	for _, t := range terms {
		if len(t) >= 4 && search.LatinWord(t) {
			projTerms = append(projTerms, t)
		}
	}
	matchedProjects := index.ProjectsMatchingTerms(dir, projTerms)
	if len(matchedProjects) == 0 {
		return
	}
	pranked, pmatched, pstrong, pnaming, pidf, perr := index.ProjectRelevantNamingCut(
		dir, matchedProjects, terms, prompt.Candidates, skip, cut)
	if perr != nil || len(pranked) == 0 {
		return
	}
	pkProj := pk.Fork(false, pidf, terms)
	for i := range pranked {
		if !projectMatchesAnyTerm(pranked[i].Project, projTerms) || !search.SpeechCarriesAnyTerm(pranked[i], terms) {
			continue
		}
		pmatched[i] = max(pmatched[i], 2)
		pstrong[i] = max(pstrong[i], 1)
		if i < len(pnaming) {
			pnaming[i] = max(pnaming[i], 1)
		}
	}
	pkProj.Pick(pranked, pmatched, pstrong, pnaming)
	pk.MergeUnscoped(&pkProj, terms)
}

// writePromptResponse prints the per-prompt answer in the shape the host
// reads: Copilot CLI's flat one under --copilot, Claude's envelope otherwise.
// A strict host gets no receipt line.
func writePromptResponse(stdout io.Writer, resp sessionStartHookResponse) {
	if mcodeHookOutput {
		emitMcodeContext(stdout, resp.HookSpecificOutput.HookEventName, resp.HookSpecificOutput.AdditionalContext)
		return
	}
	if copilotHookOutput {
		emitCopilotContext(stdout, resp.HookSpecificOutput.AdditionalContext)
		return
	}
	if strictHookOutput {
		resp.SystemMessage = ""
	}
	resp.SystemMessage = safeNote(resp.SystemMessage)
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(b))
}

// askedBeforeWhen dates the earlier question, so the reader can weigh a decision
// from yesterday against one from March. Empty when the session carries no
// date rather than printing a zero one.
func askedBeforeWhen(s model.Session) string {
	if s.Updated.IsZero() {
		return ""
	}
	return " (" + s.Updated.Local().Format("2006-01-02") + ")"
}

// widerWindow is how much further down the ranking the second pass looks when
// near-duplicates filled the first one.
const widerWindow = 4

const (
	leadTermsKept         = search.LeadTermsKept
	mediumSalientIDFFloor = 2.2
)

// promptTermsWorthAsking is the gate before the index is touched. Two terms
// were required, which silenced the sharpest prompt there is: "do we need
// pgbouncer here" reduces to one term and never reached the store, while "add
// pgbouncer to the stack" — the same question with a filler noun — did. The
// rule below already decides the single-term case (one informative match is
// served when the question named a term of art, without the déjà vu line);
// the floor returned first, so that rule could never see it.
func promptTermsWorthAsking(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	return len(terms) >= 2 || search.HasIdentifierTerm(terms)
}

func byIdentifying(terms []string, idf map[string]float64) []string {
	return search.ByIdentifying(terms, idf)
}

func symbolShaped(term string) bool { return search.SymbolShaped(term) }

func rareTerms(terms []string, idfOf map[string]float64, floor float64) []string {
	return search.RareTerms(terms, idfOf, floor)
}

func namedLeadTerms(terms []string, idfOf map[string]float64, topIDF float64) []string {
	return search.NamedLeadTerms(terms, idfOf, topIDF)
}

func latinWord(term string) bool { return search.LatinWord(term) }

func sameAnswerAs(chosen []model.Session, s model.Session, terms []string) bool {
	return search.SameAnswerAs(chosen, s, terms)
}

// openerLine pre-writes the line the agent says when it reused the recall, so
// it copies a sentence instead of composing one. Measured with a tool-using
// agent on the demo corpus (#3108): a sentence left with a hole to fill —
// "<what that session settled>" — was said in 3 of 12 replies, this complete
// one in 8 of 12 (the previous wording, same shape, 10 of 12); and asked for
// at the top of the block, before the digest, a complete sentence was said in
// none of 12. So it is complete, and it stays where it was: last, the closest
// thing to the reply.
//
// It asks for the line after the answer, never in its place (#5664). "If it
// helped, say: ..." on its own read to qwen3:8b with reasoning off as the
// whole task: in dsh, 10 of 10 replies were the line and nothing else. With
// this wording 10 of 10 answered and 10 said the line too; "end your reply
// with one line" answered 9 of 10, and dropping the line answered 6 of 10,
// because the closing sentence is also what turns a small model back to the
// question.
func openerLine(s model.Session, terms []string) string {
	return creditAsk("déjà vu: " + matchedTitle(s, terms) + " (" + provenance(s) + ") — reusing it.")
}

// creditAsk is the closing sentence of a per-prompt block: answer first, then
// the credit line.
func creditAsk(line string) string {
	return "\nAnswer the user's question in full. Then, only if this history helped, add as the last line of your reply: \"" + line + "\""
}

// repeatOpener is openerLine for a question this machine already asked: the
// line names the earlier asking outright, in the user's own words.
func repeatOpener(s model.Session, again string) string {
	again = strings.Join(strings.Fields(redact.SafeForDisplay(again)), " ")
	again = strings.ReplaceAll(again, "\"", "'")
	if len([]rune(again)) > 60 {
		again = string([]rune(again)[:60]) + "…"
	}
	return creditAsk("déjà vu: " + again + " (" + provenance(s) + ") — reusing it.")
}

// matchedTitle is the line of the session the spoken opener quotes back: what
// the digest matched, so the sentence the agent says names the thing the user
// can see. Falls through to the session's opening when nothing matched
// literally.
func matchedTitle(s model.Session, terms []string) string {
	title := search.MatchedUserLine(s, terms)
	for _, m := range s.Messages {
		if title != "" {
			break
		}
		if m.Role == "user" && !digest.IsAgentArtifact(m.Text) {
			tt := strings.TrimSpace(digest.MessageText(m.Text))
			if tt == "" || strings.HasPrefix(tt, "Exit code") {
				continue
			}
			r, _ := utf8.DecodeRuneInString(tt)
			alpha := unicode.IsLetter(r) || unicode.IsDigit(r)
			if !alpha {
				continue
			}
			title = tt
			break
		}
	}
	if title == "" {
		title = s.Title
	}
	// The digest body is display-safe (contextText strips it), but this title
	// is pulled straight from a message or the stored title into the one part
	// of the block the frame does not mark as untrusted. An escape sequence or
	// an invisible tag-block character in a hostile session's title would
	// otherwise ride into the context unaltered. Collapse whitespace too so a
	// newline cannot split the one-line opener.
	title = strings.Join(strings.Fields(redact.SafeForDisplay(title)), " ")
	// The title sits between double quotes in the line; a quote of its own
	// would close them early.
	title = strings.ReplaceAll(title, "\"", "'")
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	return title
}

// repeatLead is the lead for a question this machine has already asked. The
// claim is stronger than "a session matches" — the agent does not have to
// decide whether the history is relevant, only whether the answer still holds.
func repeatLead(s model.Session, again string) string {
	// The quoted question is pulled straight from a message; it gets the same
	// treatment as the digest body.
	again = strings.Join(strings.Fields(redact.SafeForDisplay(again)), " ")
	return "déjà vu — this was asked here before" + askedBeforeWhen(s) + ": \"" + again +
		"\". What that session settled is below; answer from it if it still holds, and say so if it does not.\n"
}

// provenance is the part of the spoken line that makes it checkable: which
// agent, when, and the session id that links the credit back to the session
// that earned it. The agent's reply is indexed like any other message, so
// `deja:<id>` in it is the only place a recall that actually helped becomes an
// observable fact; matching on title text was lexical and unreliable.
func provenance(s model.Session) string {
	return s.Harness + citationDate(s) + ", deja:" + shortID(s.ID)
}

func citationDate(s model.Session) string {
	if s.Updated.IsZero() {
		return ""
	}
	// With the year, when it is not this one. Every other date deja prints
	// carries it; the sentence the agent is told to say aloud did not, so
	// a decision from July 2025 was narrated to the user as "Jul 3" —
	// reading as five weeks ago on the one recall where the age is the
	// thing worth knowing (#R13).
	layout := "Jan 2"
	if s.Updated.Local().Year() != time.Now().Year() {
		layout = "Jan 2 2006"
	}
	return ", " + s.Updated.Local().Format(layout)
}
