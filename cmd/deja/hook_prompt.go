package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	"github.com/vshulcz/deja-vu/internal/usage"
)

// promptHookBudget keeps per-prompt injections small: this fires on every
// user message, so it must be a hint, not a payload.
//
// 1024 cut the two quoted lines mid-sentence, and what it cut was the tail —
// the paths, names and numbers an answer reuses. Measured on a real store by
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
	HookEventName string `json:"hook_event_name"`
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

// runHookPrompt is the UserPromptSubmit hook: search the user's own prompt
// against the index (relevance, not recency) and inject a compact hint only
// when something genuinely matches. Empty output means stay silent — a hook
// that talks every turn is wallpaper. It never builds or refreshes the index:
// this path runs on every prompt and must stay ~milliseconds.
func runHookPrompt(dir string, stdin io.Reader, stdout io.Writer) error {
	return runHookPromptMode(dir, stdin, stdout, false)
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
	nudge := failureNudge(dir, asked)
	// Nobody asked. A harness delivers its own plumbing as the next user turn —
	// a finished background task, a system reminder, a slash command's envelope
	// — and the hook fires on it like a question. The terms are then the
	// envelope's own field names plus an id that exists nowhere else, and what
	// they match is another notification in another session: a block of noise
	// injected as recalled history, under a line telling the user deja fires on
	// noise (#3156). Same test the digest uses on transcript messages.
	if asked == "" || digest.IsAgentArtifact(asked) {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	terms := prompt.Terms(asked)
	if !promptTermsWorthAsking(terms) {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// Version, not just presence: terms hash into buckets an index from
	// another format never wrote, so a stale store answers every prompt with
	// nothing and looks exactly like a user with no history (#777).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// A new session whose first words ask to carry on another one gets that
	// session's packet instead of a recall on the words of the request.
	if emitAskedHandoff(dir, input, asked, hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots)), plain, stdout) {
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
	// One read for every question this hook asks of the dedupe file (#3379).
	seenFile := loadSeen(dir)
	if !isSpawnedReader(input.SessionID) &&
		seenFile.asked(input.SessionID, askKey(terms), repeatAskWindow) {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// The payload first, then the export, then where the process stands: the
	// export is set once per process and will not change, so reading it alone
	// answered a second payload with the first one's project (#2182).
	cwd := hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots))
	// Rank THIS project's sessions by how well they match the prompt terms
	// (IDF-weighted), rather than reconstructing an AND query — natural
	// prompts are full of filler that poisons an AND.
	// The sessions this agent session has already been shown, and the one it is
	// writing, are discarded a few lines below on their id alone. Handing them
	// to the ranking means they are never read: on a real store that is 15 of
	// 26 candidates, each read from disk in full before being dropped.
	// What this agent session was already shown, by the text of the block
	// rather than by the session it came from. Banning the session banned
	// everything else it holds: measured on a real store, walking one working
	// session of eighty messages, the second half got three blocks where the
	// same messages without the ban got thirty-eight, and only seven of those
	// were word-for-word repeats. The same past session answers many questions
	// in a day, and after the first answer it went silent.
	seen := seenFile.injected(input.SessionID)
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
	names := digest.ProjectNameCandidates(cwd)
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
	// The asker and what counts as it: a spawn's parent, a sub-agent's parent,
	// the sub-agents it spawned (#4548), a fork's source (#4549).
	self, cut := askerLineageCut(dir, input.SessionID, input.TranscriptPath, input.ParentSessionID)
	for id := range self {
		skip[id] = true
	}
	promptStart := time.Now()
	const promptSoftBudget = 1500 * time.Millisecond
	ranked, matched, strong, naming, idfOf, err := index.ProjectRelevantNamingCut(dir, names, terms, prompt.Candidates, skip, cut)
	// The rule every other surface applies: a promoted note goes in front of
	// the transcript it was distilled from. This hook ranks sessions and never
	// builds a search.Hit, so it had no note-over-source rule at all and the
	// two slots below could go to the transcript while its own note waited
	// behind it (#2803).
	search.LiftNoteSessionsAboveTheirSource(ranked)
	rankedAlreadyShown = 0
	for _, s := range ranked {
		if skip[s.ID] {
			rankedAlreadyShown++
		}
	}
	if err != nil || len(ranked) == 0 {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// The one word of the question that identifies something, kept apart for
	// the test below: asking whether any term was spoken lets "показал" answer
	// for "v11", and nearly every session says a word like that.
	// The words of the question that identify something, kept apart for the
	// test below: asking whether ANY term was spoken lets "показал" answer for
	// "v11", and nearly every session says a word like that.
	//
	// One word was too few. Rareness is measured against this corpus, and in a
	// small or homogeneous one it crowns whichever ordinary noun happens to be
	// uncommon: on a seeded store the question "the orders service runs out of
	// db connections" led on "service", so the session that settled it — which
	// says "orders worker" and never "service" — was dropped at this gate
	// while a neighbour about the reporting service was served in its place.
	// The ranking had already put the right one first with two strong terms.
	leadTerms := terms
	if ordered := byIdentifying(terms, idfOf); len(ordered) > 0 {
		leadTerms = ordered[:min(len(ordered), leadTermsKept)]
	}
	pol := policy.Load()
	pk := &promptPicker{
		pol:       pol,
		terms:     terms,
		idfOf:     idfOf,
		topIDF:    index.TopIDF(dir),
		self:      self,
		leadTerms: leadTerms,
		unscoped:  len(names) == 0,
		deadline:  promptStart.Add(promptSoftBudget),
		ss:        make([]model.Session, 0, 2),
	}
	pk.pick(ranked, matched, strong, naming)
	// One slot left and duplicates took the window: ask for a wider one. The
	// second pass costs a ranking read and happens only when the store said
	// the same thing several times, which is exactly when the answer is
	// further down than the window reached. Skip the wider retry when the soft
	// latency budget is already exhausted, and exclude the candidates already
	// examined in the first pass (#5901).
	if len(pk.ss) < 2 && pk.crowdedOut > 0 && time.Since(promptStart) < promptSoftBudget {
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
			pk.pick(wider, wmatched, wstrong, wnaming)
		}
	}
	ss, confident, worthDigest := pk.ss, pk.confident, pk.worthDigest
	if len(ss) == 0 {
		return nil
	}
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not, so the
	// concluded_session arm scored a behaviour the product never had.
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not, so the
	// concluded_session arm scored a behaviour the product never had.
	// Of the two candidates, lead with the one that settled something about the
	// question rather than the one that merely mentioned it. The benchmark has
	// been applying this since the arm was written — and saying so in a comment
	// that claimed the hook did the same — while the hook did not.
	ss = search.LeadWithConclusion(ss, terms)
	for i := range ss {
		index.MarkUserRules(dir, &ss[i], names)
	}
	// A rejected session is not an equal answer, and the mark has to travel
	// with it into the block the agent reads (#761).
	ss, rejectedWarning := orderForInjection(ss)

	// "You have been here" on the strength of one word teaches the user to
	// ignore the line. The recall itself still goes in.
	// Not on a host that shows it to nobody: the window it keeps is spent.
	showLine := confident && notesHaveReader() && dejaVuLineDue(dir, input.SessionID)
	// The payload is sized to the claim. Two informative terms is a real match
	// and earns the digest; a single rare term is a hint, and a hint that costs
	// a full digest on every message is most of what deja spends. The pointer
	// keeps it discoverable — the agent learns there is history here and can
	// ask for it — for 134 bytes against the 0.5-1.3 KB a real digest measures.
	var body string
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
	if worthDigest && (isSpawnedReader(input.SessionID) || digestBudgetSpent(seen, input.SessionID)) {
		if cite := ss[0]; search.AskedBefore(cite, terms) == "" {
			worthDigest = false
		}
	}
	fullDigest := worthDigest
	if worthDigest {
		full, ok := promptDigestBody(dir, input.SessionID, ss, confident, terms, idfOf, asked, nudge, rejectedWarning)
		if !ok {
			return emitNudgeOnly(stdout, plain, nudge)
		}
		body = full
	} else {
		body = weakRecallPointer(ss, terms) + shownInjectionWarning(ss, ss[:1])
		if nudge != "" {
			body += "\n" + nudge
		}
	}
	// The same block twice is wallpaper, and the reader stops looking. Judged
	// on what would be shown, so a session whose other lines answer the next
	// question still gets to answer it.
	fp := blockFingerprint(body)
	if seen[fp] || seen[digestSeenPrefix+fp] {
		return emitNudgeOnly(stdout, plain, nudge)
	}
	if fullDigest {
		fp = digestSeenPrefix + fp
	}
	// And the same sentence twice under another session's id is the same
	// wallpaper: the fingerprint above covers the block, whose header carries
	// the id and the date, so a fork or a subagent transcript of the same work
	// passes it every time. Measured on a real store over eight near-identical
	// nudges: nine sessions cited, fifteen quoted lines, eleven of them
	// distinct, and the worst line four times under four ids.
	filtered, freshQuotes, anyFresh := withoutSeenQuotes(seen, body)
	switch {
	case anyFresh:
		body = filtered
	case !strings.HasPrefix(input.SessionID, spawnReaderPrefix):
		// Every quote was delivered to this reader already, so the block is the
		// same injection wearing another session's header.
		return emitNudgeOnly(stdout, plain, nudge)
	}
	// A spawned agent is a new reader even when the parent has heard it all: the
	// key it recalls under is the parent plus the instructions, which exists to
	// stop a re-spawn of the same agent repeating itself — not to send the
	// second one in blind. Handing it what the parent saw is the whole point.
	out := frameRecall(body)
	rememberInjectedIDs(dir, input.SessionID, fp)
	rememberInjectedIDs(dir, input.SessionID, freshQuotes...)
	// Stamped, because the question is answered again once the window passes,
	// where a block fingerprint holds for the life of the session.
	rememberInjectedIDsFor(dir, input.SessionID, "", []string{askKey(terms)})
	rememberInjectedFor(dir, input.SessionID, projectKey, ss)
	return emitPromptBlock(stdout, dir, input.SessionID, out, ss, terms, unreadable, plain, showLine)
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
	// Hand the digest the query's words most-identifying first. It has no
	// way of its own to tell "mm_status" from "decide", and the session
	// that answers often says both — measured live, ten of the answers
	// this hook newly returns open on the ordinary word.
	digest, shown := search.AutoRecallDigestShowing(ss, digestBudget(confident), byIdentifying(terms, idfOf), asked)
	if strings.TrimSpace(digest) == "" {
		return "", false
	}
	// The sessions passed on where their words were said, but the block is
	// what the agent reads. When no line it quotes holds two of the question's
	// words — unless the question named a single rare subject and the quote
	// holds that subject — each line is about something else that shares one
	// word with it. When the question joins two rare subjects, the block must
	// also cover them rather than answering a two-subject question with a
	// session that only ever heard of one.
	named := namedLeadTerms(terms, idfOf, index.TopIDF(dir))
	if len(named) == 1 {
		if !search.QuoteCarriesTerms(digest, named, 1) && !search.QuoteCarriesTerms(digest, terms, min(2, len(terms))) {
			return "", false
		}
	} else if !search.QuoteCarriesTerms(digest, terms, min(2, len(terms))) {
		return "", false
	}
	// The line the agent is asked to say names a session; it has to be one
	// the digest shows. The top of the ranking is skipped when it has
	// nothing quotable, and the opener then credited a session that was
	// not in the block.
	cite := ss[0]
	if len(shown) > 0 {
		cite = shown[0]
		rejectedWarning = shownInjectionWarning(ss, shown)
	}
	tail := openerLine(cite, terms)
	if nudge != "" {
		tail += "\n" + nudge
	}
	lead := promptHookLead
	// After the first block of a session the two explanatory sentences are
	// words the agent has already read, and the third repeats the line this
	// block ends with. Measured on a real store: a session receives a median
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
	if blockAlreadySentThisSession(dir, sessionID) {
		lead = ""
	}
	// A repeat of the question itself is a different claim than a session
	// about the subject, and a stronger one: the agent does not have to
	// decide whether the history is relevant, only whether the answer
	// still holds. Measured over this machine's own sessions, 6.5% of
	// substantial questions are asked again in a later session, and the
	// exact-match counter in `deja stats` sees a fifth of them.
	if again := search.AskedBefore(cite, terms); again != "" {
		lead = repeatLead(cite, again)
		tail = repeatOpener(cite, again)
		if nudge != "" {
			tail += "\n" + nudge
		}
	}
	return lead + rejectedWarning + digest + tail, true
}

// promptPicker chooses the sessions the prompt hook injects, at most two,
// and keeps what the choice says about the match.
type promptPicker struct {
	pol   policy.Policy
	terms []string
	idfOf map[string]float64
	// topIDF is the idf a word said in one session reaches on this store
	// (index.TopIDF), so a small store's rarest words still name something.
	topIDF    float64
	self      map[string]bool
	leadTerms []string
	unscoped  bool

	ss        []model.Session
	confident bool
	// worthDigest is the payload decision, kept apart from the announcement.
	// A word rare enough to identify something is a real match — the bar above
	// says so and lets such a question through — but saying "you have been here"
	// on one word teaches the reader to ignore the line, so that stays at two.
	worthDigest bool
	// How many candidates were dropped for saying what one already chosen
	// says. A store repeats itself — the same complaint filed against forty
	// shards — and those copies take the ranking's window with them, so the
	// answer to the question can sit outside it (#1556).
	crowdedOut int
	deadline   time.Time
}

// pick walks one ranking and takes the sessions worth injecting.
func (p *promptPicker) pick(ranked []model.Session, matched, strong, naming []int) {
	for i, s := range ranked {
		if len(p.ss) > 0 && !p.deadline.IsZero() && time.Now().After(p.deadline) {
			return
		}
		// Every other injection path asks the policy first; this one is a
		// per-prompt injection like any other, and imported projects reach
		// it (a local project name is a substring of "imported:<name>").
		if !p.pol.Allows(policy.ActivationAuto, s.Project) {
			continue
		}
		// A scheduled job's run is searchable, not offered unasked: its
		// prompt is the same every fire, and what it carries in the user
		// role is often someone else's text (#4948).
		if s.Kind == "cron" {
			continue
		}
		// matched counts informative terms; strong counts the rare ones — a
		// term that identifies something on its own rather than merely beating
		// the corpus average. Two informative terms earn the announcement. A
		// single term is a weak claim, so it only injects when that term is
		// strong: "pgbouncer" answers a question, "problem" does not, and this
		// hook pays its cost on every message the user sends. Measured on
		// cross-paired prompts whose answer is absent, the old bar injected on
		// 94% of them; half of those rested on one ordinary word.
		named := -1
		if i < len(naming) {
			named = naming[i]
		}
		if !search.RecallWorthShowingNaming(p.terms, matched[i], strong[i], named, p.idfOf) {
			continue
		}
		// A word rare enough to identify something is a real match on its own —
		// the bar just above says so in as many words, and lets such a question
		// through. The payload rule did not: it asked only for two informative
		// terms, so whether the reader got the memory or a pointer to it turned
		// on whether some ordinary word of the question happened to also appear
		// in that session.
		//
		// Measured on a real store over sixty subjects the project holds:
		// "напомни, что мы решали про X" returned content 63% of the time and
		// "what did we decide about X" returned it never — the same question,
		// the same answer sitting in the same session.
		if matched[i] >= 2 {
			p.confident = true
		}
		if matched[i] >= 2 || strong[i] >= 1 {
			p.worthDigest = true
		}
		// The session being written right now is never worth recalling to
		// itself. Work merely fresh is a different case: "what did we just
		// change" is a question about the last ten minutes, so age alone no
		// longer withholds an answer — it only withholds the unprompted
		// déjà vu line below.
		//
		// Asked before the narrowing below, not after: narrowing scans every
		// message, the session being written is usually the longest one on the
		// store, and it was scanned in full only to be dropped on the next
		// line. Measured on a 1149-session store, moving these two checks up
		// takes the hook's median from 181 ms to 136 ms.
		if p.self[s.ID] {
			continue
		}
		if !p.leadTermsMeet(s) {
			// The subject matched, but only where a tool printed it: a
			// failing job line, a bumped dependency, a pinned action
			// version. Nobody in that session said anything about it, and a
			// block built from those lines answers a different question
			// than the one asked.
			continue
		}

		// A marathon session that touched everything matches everything, so
		// it used to be skipped whole. But the sessions people ask about are
		// exactly the long ones — measured here, only 2% of sessions cross
		// the line and they are the current work. The haystack argument is
		// about the session as a whole and not about the part that matched,
		// so narrow it to that part instead of dropping the answer.
		if len(s.Messages) > dejaVuMaxMessages || s.Held > dejaVuMaxMessages {
			s = focusSession(s, p.terms)
			if len(s.Messages) == 0 {
				continue
			}
		}
		// Two slots, two answers. The same content reaches the block from
		// several sessions all the time — a marathon that was split, a
		// resumed session, a workflow run again — and spending both slots on
		// it costs the reader the second answer entirely. Measured on a
		// seeded store where the question drifted from the wording of the
		// session that settled it, both slots went to two copies of one
		// neighbour on every framing but the near-verbatim one.
		if sameAnswerAs(p.ss, s, p.terms) {
			p.crowdedOut++
			continue
		}
		p.ss = append(p.ss, s)
		if len(p.ss) == 2 {
			return
		}
	}
}

// leadTermsMeet asks for the question's lead words where someone spoke them,
// and for two of them in one message unless a lead word names something.
//
// The ranking counts a word anywhere in a session, and a long session holds
// every word of any question somewhere, each in a different turn about a
// different thing. Replayed over 80 real prompts, 50 blocks had nothing to
// do with the question, most of them from sessions of thousands of messages
// matched that way.
//
// A word names something when it is shaped like a symbol, a path or a version,
// or is a Latin-letter word, and the store holds it rarely
// (search.IdentifyingTerms): "winget" or "pgbouncer" is answered by the
// session that said it once. A word of an inflected language is never enough
// alone, because each form of an everyday word is rare in any store.
//
// Asking a long session for three words together instead of two was
// measured too: it emptied 2 of 5 blocks that answered the prompt.
func (p *promptPicker) leadTermsMeet(s model.Session) bool {
	if fixedNamed := namedLeadTerms(p.leadTerms, p.idfOf, 1e9); len(fixedNamed) >= 2 {
		for _, t := range fixedNamed {
			if !search.SpeechCarriesAnyTerm(s, []string{t}) {
				return false
			}
		}
		if len(s.Messages) >= dejaVuMaxMessages || s.Held >= dejaVuMaxMessages {
			return search.SpeechCarriesTermsTogether(s, fixedNamed, 2)
		}
		return true
	}
	if named := namedLeadTerms(p.leadTerms, p.idfOf, p.topIDF); len(named) > 0 && search.SpeechCarriesAnyTerm(s, named) {
		return true
	}
	if p.unscoped {
		return false
	}
	return search.SpeechCarriesTermsTogether(s, p.leadTerms, 2)
}

func namedLeadTerms(terms []string, idfOf map[string]float64, topIDF float64) []string {
	var named []string
	for _, t := range search.IdentifyingTerms(terms, idfOf, topIDF) {
		if symbolShaped(t) || latinWord(t) {
			named = append(named, t)
		}
	}
	return named
}

// latinWord reports a term written in Latin letters, the script tool and
// product names are written in.
func latinWord(term string) bool {
	for _, r := range term {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
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

// sameAnswerAs reports whether this session would say what one already chosen
// says. Judged on the lines the question actually matched rather than on the
// whole transcript: two sessions of the same workflow differ everywhere else
// and agree exactly where it matters.
func sameAnswerAs(chosen []model.Session, s model.Session, terms []string) bool {
	next := matchedWords(s, terms)
	if len(next) == 0 {
		return false
	}
	for _, c := range chosen {
		if sameWords(matchedWords(c, terms), next) {
			return true
		}
	}
	return false
}

// sameWords reports whether two sessions said the same thing about the
// question. An exact hash of the matching lines missed the case it exists for:
// the same complaint filed twice differs by a shard number, a pid, a run id,
// and hashing put the two copies in the block side by side while the session
// that answered the question never appeared (#1556). The threshold matches the
// one the session-start digest already uses on its own candidates.
func sameWords(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	shared := 0
	for w := range b {
		if a[w] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	return union > 0 && float64(shared)/float64(union) >= sameAnswerOverlap
}

// sameAnswerOverlap is how much of what two sessions said about the question
// has to be the same word before the second one is not worth a slot.
const sameAnswerOverlap = 0.8

// matchedWords is the words of the session's matching lines. Empty when
// nothing matched, which is not a duplicate of anything.
func matchedWords(s model.Session, terms []string) map[string]bool {
	// Lines that carry the question, not lines that brush it. A session says
	// the thing once and talks around it for the rest, and the talk differs
	// session to session — so hashing every line that held any query word gave
	// two sessions that settled the same thing two fingerprints, and the block
	// spent both its slots saying it twice. A line holding one ordinary word of
	// the question is that talk.
	words := map[string]bool{}
	for _, m := range s.Messages {
		fields := strings.Fields(strings.ToLower(m.Text))
		if search.TermHitsLowered(strings.Join(fields, " "), terms) < fingerprintTermsPerLine {
			continue
		}
		if !search.SpeechCarriesAnyTerm(model.Session{Messages: []model.Message{m}}, terms) {
			continue
		}
		for _, w := range fields {
			words[w] = true
		}
	}
	return words
}

// fingerprintTermsPerLine is how much of the question a line has to carry
// before it counts toward what a session is saying.
const fingerprintTermsPerLine = 2

// widerWindow is how much further down the ranking the second pass looks when
// near-duplicates filled the first one.
const widerWindow = 4

// leadTermsKept is how many of the question's identifying words a session may
// be judged on. Three rather than one: the gate exists to reject a session
// that matched only where a tool printed the subject, and three of the rarest
// words still keep the ordinary ones out.
const leadTermsKept = 3

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

// byIdentifying orders the query's terms by what the index says each is worth,
// rarest first, so a caller choosing one line out of a session can prefer the
// word that identified the match over the word that merely appeared in it.
func byIdentifying(terms []string, idf map[string]float64) []string {
	if len(idf) == 0 || len(terms) < 2 {
		return terms
	}
	out := append([]string(nil), terms...)
	sort.SliceStable(out, func(i, j int) bool {
		if idf[out[i]] != idf[out[j]] {
			return idf[out[i]] > idf[out[j]]
		}
		// A store of one or two sessions collapses every ratio to zero, so the
		// terms all tied and the word the session had to have spoken was
		// whichever the reader typed first — "seeing", in a question about
		// gateway_timeout. The only session such a store holds was then
		// dropped for not saying it (#2257). The rest of the ranking already
		// treats a tiny corpus as a special case; this is that case for the
		// lead term: prefer the word that reads like a term of art.
		// Shape before length, or "dashboards" would out-rank "s3": a word
		// carrying an underscore, a dot, a slash or a digit names something
		// whatever its length, and length is only the tiebreak among words
		// that look alike.
		if a, b := symbolShaped(out[i]), symbolShaped(out[j]); a != b {
			return a
		}
		if a, b := identifying(out[i]), identifying(out[j]); a != b {
			return a
		}
		return utf8.RuneCountInString(out[i]) > utf8.RuneCountInString(out[j])
	})
	return out
}

// identifying reports whether one term reads like a term of art — the same
// test the recall bar applies to a whole question, asked of a single word. It
// admits any ordinary word of three letters, so the length below settles the
// ties it leaves: "gateway_timeout" over "seeing".
func identifying(term string) bool { return search.HasIdentifierTerm([]string{term}) }

// symbolShaped reports whether a term is punctuated or numbered the way a
// symbol, a path or a version is — "gateway_timeout", "pkg/index", "v11", "s3".
func symbolShaped(term string) bool {
	for _, r := range term {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
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
