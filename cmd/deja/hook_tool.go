package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja hook-tool` is recall at the moment of the action rather than at the
// moment of the sentence.
//
// Every other injection point deja has fires on text a person wrote. But an
// agent spends its time running commands and editing files, and that is where
// the machine's own history is both cheapest to match — an exact command, an
// exact path, no lexical guessing — and most likely to save work. On a real
// 1165-session store, 335 commands were run in three or more separate sessions
// and 487 files were touched in five or more.
//
// The cost side is the reason this stays lean. A prompt hook is paid once per
// message; this is paid once per action, and there are an order of magnitude
// more of those. So: at most one line carrying at most one prior decision, no
// digest, no snippets, and silence unless the history is a pattern rather than a
// coincidence. A measured A/B settled the payload: a line that only pointed at
// `deja blame` changed nothing an agent did, while the same line carrying the
// decision drove it to reuse the prior fix — so the file line names the decision
// and keeps the pointer only as a fallback.

const (
	// toolHookMaxBytes caps the whole payload. Wider than a bare pointer because
	// the file line now carries one prior decision, which the measurement showed
	// is what makes the difference; still one line, still per-action cheap.
	toolHookMaxBytes = 480
	// toolHookDecisionBudget bounds the extracted decision inside that cap.
	toolHookDecisionBudget = 200
	// toolHookDecisionScan caps how many of the newest sessions the file line
	// reads for a decision — this runs inside an action the agent waits on.
	toolHookDecisionScan = 3
	// toolHookMsgTail caps the messages scanned per session for the same reason:
	// a decision states itself near the end, not buried in a long transcript.
	toolHookMsgTail = 150
	// toolHookMinFileSessions is when a file's history stops being noise. A
	// file two sessions touched is ordinary work; five is a place with a past.
	toolHookMinFileSessions = 5
)

type toolHookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

func runHookTool(dir string, stdin io.Reader, stdout io.Writer) error {
	raw := readHookPayload(stdin, hookStdinWait)
	var input toolHookInput
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&input)
	// Spawning an agent is the one action whose reply has to reach someone
	// other than the caller, so it answers in its own shape. See hook_spawn.go.
	if isSpawnTool(input.ToolName) {
		if !planIndexReady(dir) {
			return nil
		}
		return runHookSpawn(dir, input, raw, stdout)
	}
	// Never build or repair from here. This runs inside an action the user is
	// waiting on, and a miss costs nothing while a rebuild costs seconds.
	if !planIndexReady(dir) {
		return nil
	}
	line := toolHookLine(dir, hookCWD(input.CWD), input)
	if line == "" {
		return nil
	}
	// A PreToolUse hook fires on every action, so the same fact must not be
	// re-injected turn after turn. Dedupe per agent session on the line itself,
	// the way hook-plan and hook-prompt dedupe what they inject.
	token := "tool:" + shortHash(line)
	if alreadyInjected(dir, input.SessionID)[token] {
		return nil
	}
	rememberInjectedIDs(dir, input.SessionID, token)
	line = truncateToolLine(line, toolHookMaxBytes)
	out := frameRecall(line)
	// Record the injection so deja's most frequent surface is not invisible to
	// stats and the receipt. Deduped above, so this counts a distinct fact
	// served, not every action.
	usage.RecordResult(dir, usage.KindTool, len(out), 1, false)
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "PreToolUse"
	resp.HookSpecificOutput.AdditionalContext = out
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// toolHookLine is what the agent is told, or "" for the silence that is the
// default answer. It gates on the tool: a command line is worth saying only for
// the tool that runs a command, and a file's history only before the file is
// changed — Read, Glob and NotebookRead carry a file_path too, and a hook wired
// with a wide matcher would otherwise fire on every one of them.
func toolHookLine(dir, cwd string, input toolHookInput) string {
	switch input.ToolName {
	case "Bash":
		if cmd := strings.TrimSpace(input.ToolInput.Command); cmd != "" {
			return commandHookLine(dir, cwd, cmd)
		}
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		if path := strings.TrimSpace(input.ToolInput.FilePath); path != "" {
			return fileHookLine(dir, cwd, path)
		}
	case "apply_patch":
		// Codex and other OpenAI-style agents make every file edit through a
		// single apply_patch tool: the patch text is in `command` and the path
		// is in its header, not in file_path. Pull the targets out so the
		// file-history line fires here too — without this the hook is blind to
		// every edit those agents make.
		for _, path := range applyPatchFiles(input.ToolInput.Command) {
			if line := fileHookLine(dir, cwd, path); line != "" {
				return line
			}
		}
	}
	return ""
}

// applyPatchFiles pulls the target paths out of an apply_patch body. Each file
// section is headed by "*** Add File: p", "*** Update File: p" or "*** Delete
// File: p", and a rename adds "*** Move to: p".
func applyPatchFiles(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, pfx := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, pfx) {
				if p := strings.TrimSpace(line[len(pfx):]); p != "" {
					out = append(out, p)
				}
				break
			}
		}
	}
	return out
}

func commandHookLine(dir, cwd, cmd string) string {
	// "You have run this before" is worthless for an inspection command the
	// agent runs constantly — git status, git diff, ls, cat. On a real store
	// these are the top of the table (git status --short in 116 sessions), and
	// a line about them on every action is pure noise. The value is in a build,
	// a test, a deploy — a command that does something.
	if index.InspectionCommand(cmd) {
		return ""
	}
	use, ok := index.CommandHistory(dir, cmd)
	if !ok {
		return ""
	}
	// A hook that fires unasked is the auto activation. Count only the sessions
	// in projects the trust policy allows — and take the last-run date from
	// those projects too, so a command surfaced from an allowed project does
	// not print a withheld project's more-recent run. An index built before
	// ByProject existed has none; the version gate keeps the hook silent there
	// rather than falling back to a machine-wide count that ignores the policy.
	if len(use.ByProject) == 0 {
		return ""
	}
	pol := policy.Load()
	sessions := 0
	var last time.Time
	for proj, pu := range use.ByProject {
		if !pol.Allows(policy.ActivationAuto, proj) {
			continue
		}
		sessions += pu.Sessions
		if pu.Last.After(last) {
			last = pu.Last
		}
	}
	if sessions < 1 {
		return ""
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	head := fmt.Sprintf("This machine has run that command in %s%s",
		toolSessionCount(sessions), when)
	// The file path carries the prior decision rather than a pointer to it,
	// for the measured reason recorded there — a line that only says history
	// exists changes nothing. A command deserves the same: before `npm run
	// build`, what matters is that it failed here last time and why, not that
	// it has been run twice.
	// A decision the user promoted about this very command comes first. The
	// ranking below asks whether a session's words match the command's, which
	// on a store where several sessions ran it makes those words too common to
	// clear the idf floor — measured: every candidate at 0 informative terms,
	// so the scan never ran and the count printed alone. Whether the promoted
	// session ran the command is a fact rather than a ranking (#2516).
	if d := promotedCommandDecision(dir, cwd, cmd); d != "" {
		return head + " — last time: " + d
	}
	if d := commandDecisionLine(dir, cwd, cmd); d != "" {
		return head + " — last time: " + d
	}
	return head + "."
}

// promotedCommandDecision is the decision this project promoted about the
// command about to run, or "" when there is none. It reads the promoted
// sessions of the project — few, and bounded here — and asks each whether it
// ran this command, which is the same question CommandHistory answers in
// aggregate.
//
// What that costs, measured on a store the size of a working machine (1,500
// sessions, the command run in 188 of them): this path and the ranking path
// below add 3–5 ms to a hook that already cost ~19 ms, six session reads in the
// worst case, three per path (#2522). In the common case the two read the same
// sessions — one to ask whether a promoted note belongs to it, one to ask
// whether it ran the command — and passing the session between them would halve
// that. Not done: 5 ms is not worth the coupling, and this is where a reader
// would look for the number.
func promotedCommandDecision(dir, cwd, cmd string) string {
	names := digest.ProjectNameCandidates(cwd)
	if len(names) == 0 {
		return ""
	}
	pol := policy.Load()
	notes := importedConventions(names)
	for _, n := range sources.LoadPromotedNotes() {
		if n.State != "accepted" || !inAnyProject(n.Project, names) {
			continue
		}
		if n.Project != "" && !pol.Allows(policy.ActivationAuto, n.Project) {
			continue
		}
		notes = append(notes, n)
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	read := 0
	for _, note := range notes {
		if read >= toolHookDecisionScan {
			break
		}
		harness, id, ok := strings.Cut(note.Session, ":")
		if !ok {
			continue
		}
		read++
		s, found, err := index.FindByIdentity(dir, harness, id)
		if err != nil || !found || !index.SessionRanCommand(s, cmd) {
			continue
		}
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = strings.TrimSpace(note.Title)
		}
		if text == "" {
			continue
		}
		return trimTrailingFragment(search.SafeText(clipDecision(text, toolHookDecisionBudget)))
	}
	return ""
}

// commandDecisionLine returns what happened the last time this command ran, or
// "" when the history holds no conclusion about it.
//
// The command's sessions are found by searching for it: the manifest records
// the files a session touched but not the commands it ran, so there is no
// cheaper lookup, and this hook fires on a build or a deploy rather than on
// every message — the prompt hook already pays a search per keystroke.
func commandDecisionLine(dir, cwd, cmd string) string {
	terms := prompt.Terms(normalizedCommandText(cmd))
	if len(terms) == 0 {
		return ""
	}
	ranked, _, _, _, err := index.ProjectRelevant(dir, digest.ProjectNameCandidates(cwd), terms, toolHookDecisionScan)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	states := sources.PromotedLifecycles()
	for _, s := range ranked {
		// The old rule wanted every one of the command's words to be rare.
		// Measured on 1,696 sessions, none of the fifteen most-run commands
		// clears that: a command's words are common by construction, which is
		// what makes it one this machine runs. `go test ./...` reduces to the
		// single term "test", and no bar built on rarity admits it (#2520).
		//
		// What the rule was after — is this session about this command — is a
		// fact rather than a ranking: did the session run it. The words still
		// choose the candidates; running the command earns the line. A session
		// that merely says "apply" is not the history of `terraform apply`.
		if !pol.Allows(policy.ActivationAuto, s.Project) || !decisionUsable(s, states) {
			continue
		}
		// The ranked sessions are served without their command records, so the
		// question is asked of the session as the index holds it. Bounded by
		// the same scan the ranking is.
		whole, ok, ferr := index.FindByIdentity(dir, s.Harness, s.ID)
		if ferr != nil || !ok || !index.SessionRanCommand(whole, cmd) {
			continue
		}
		if len(s.Messages) > toolHookMsgTail {
			s.Messages = s.Messages[len(s.Messages)-toolHookMsgTail:]
		}
		if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
			return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0])))
		}
	}
	return ""
}

// normalizedCommandText is the command as words to search for: the invocation
// without its flags, which are punctuation to the tokeniser and noise to the
// ranking.
func normalizedCommandText(cmd string) string {
	var out []string
	for _, f := range strings.Fields(cmd) {
		if strings.HasPrefix(f, "-") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func fileHookLine(dir, cwd, path string) string {
	// FileSessions matches on the file's basename, so without scoping "main.go"
	// or "README.md" collects every project's file of that name — the line then
	// claims a history this file does not have and points `deja blame` at a
	// pile of other repos. Count only sessions in the project being worked in,
	// unless the stored path is the exact one (which cannot collide).
	projects := digest.ProjectNameCandidates(cwd)
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	var inScope []index.SessionMeta
	for _, meta := range index.FileSessions(dir, path) {
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !fileMetaInScope(meta, path, projects) {
			continue
		}
		sessions++
		inScope = append(inScope, meta)
		if meta.Updated.After(last) {
			last = meta.Updated
		}
	}
	if sessions < toolHookMinFileSessions {
		return ""
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	// The line-safe form: the name lands inside the hook's own sentence, and a
	// newline in it would end that sentence and start one that reads as deja
	// speaking to the agent (#1863).
	name := search.SafePath(baseName(path))
	head := fmt.Sprintf("%s has been worked on in %s%s", name, toolSessionCount(sessions), when)
	// The measured difference between a nudge that changes what an agent does
	// and one it ignores is whether it carries the decision or only points at
	// it: a line that said "deja blame X has the history" drove no reuse, while
	// the same moment carrying the prior decision did. So surface the decision
	// here, and fall back to the pointer only when none can be extracted.
	// Named for what it is. A promoted note is the user's own decision; a line
	// the conclusion scan found is the last thing a session said about the
	// file, which is often "changed the renderer (5)". The line was built on a
	// measurement — an agent follows a decision where it ignores a pointer —
	// and calling filler a decision spends exactly that credibility (#2526).
	// The command line has said the weaker "last time:" all along.
	if d := promotedDecisionFor(inScope); d != "" {
		return head + " — prior decision: " + d
	}
	if d := fileDecisionLine(dir, inScope); d != "" {
		// A scanned line is called a decision only when it reads as one. The
		// scan's own fallback is the newest session's closing sentence, which
		// is as often "changed the renderer (5)" as it is a decision, and the
		// same marker list the digest uses can tell them apart.
		if digest.CarriesDecision(d) {
			return head + " — prior decision: " + d
		}
		return head + " — last session on it ended: " + d
	}
	return fmt.Sprintf("%s — `deja blame %s` has the history.", head, name)
}

// fileDecisionLine returns the single most relevant prior decision recorded
// about this file, or "" if none can be extracted. It reads the newest in-scope
// sessions (bounded, newest first) and takes the first that yields a conclusion
// and was not later taken back — the same decision-carrying line the SessionStart
// digest surfaces, but delivered at the moment the file is about to change.
func fileDecisionLine(dir string, metas []index.SessionMeta) string {
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	// A promoted note first. It is the user's own statement about this work,
	// which is why the session-start block leads with it and calls it standing;
	// scanning for a conclusion instead handed back whatever the newest session
	// happened to end with — "looked at retry.go again (5)" in front of "the
	// retry budget stays at 5" (#2495).
	if line := promotedDecisionFor(metas); line != "" {
		return line
	}
	states := sources.PromotedLifecycles()
	for i, meta := range metas {
		if i >= toolHookDecisionScan {
			break
		}
		s, ok, err := index.FindByIdentity(dir, meta.Harness, meta.ID)
		if err != nil || !ok || !decisionUsable(s, states) {
			continue
		}
		// Bound the work inside a hook the agent is blocked on: a marathon
		// session's whole history is not worth scanning for one line, and a
		// decision states itself near the end anyway.
		if len(s.Messages) > toolHookMsgTail {
			s.Messages = s.Messages[len(s.Messages)-toolHookMsgTail:]
		}
		if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
			return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0])))
		}
	}
	return ""
}

// promotedDecisionFor returns the newest accepted promoted note belonging to one
// of these sessions. Accepted only, for the reason projectConventions gives: a
// decision later reversed carries a non-accepted state, and LoadPromotedNotes
// keeps the latest state per source.
func promotedDecisionFor(metas []index.SessionMeta) string {
	if len(metas) == 0 {
		return ""
	}
	want := make(map[string]bool, len(metas))
	noteKeys := make(map[string]bool, len(metas))
	for _, meta := range metas {
		want[meta.Harness+":"+meta.ID] = true
		noteKeys["deja-note-"+meta.Harness+"-"+meta.ID] = true
		if meta.OrigID != "" {
			want[meta.Harness+":"+meta.OrigID] = true
			noteKeys["deja-note-"+meta.Harness+"-"+meta.OrigID] = true
		}
	}
	// The note's own project as well as the session it came from. The metas
	// are already scoped by the auto activation, so a note is reached only
	// through an allowed session — but the note carries a project of its own,
	// and every other reader of these notes checks it (#2506).
	pol := policy.Load()
	var found []sources.PromotedNote
	keep := func(note sources.PromotedNote) {
		if note.State != "accepted" {
			return
		}
		if note.Project != "" && !pol.Allows(policy.ActivationAuto, note.Project) {
			return
		}
		found = append(found, note)
	}
	for _, note := range sources.LoadPromotedNotes() {
		if !want[note.Session] {
			continue
		}
		keep(note)
	}
	// And the decisions that arrived by sync. Those are not in this machine's
	// notes file at all — a promotion crosses as a session of its own, carrying
	// the state, which is how the view page reads them (#2421). It touched no
	// file, so it is never among the sessions this file has; what ties it back
	// is the id a note is built from, `deja-note-<harness>-<id>`, derived here
	// from the session rather than parsed out of the note (harness names carry
	// dashes, so the id does not invert). Without this a decision promoted on
	// one machine was a decision in search on the other and filler prose at the
	// moment of the edit (#2510).
	for _, meta := range metas {
		if meta.Lifecycle != "" {
			keep(noteFromMeta(meta))
		}
	}
	if len(noteKeys) > 0 {
		for _, note := range index.PromotedNoteMetas(index.DefaultDir(), func(project string) bool {
			return pol.Allows(policy.ActivationAuto, project)
		}) {
			id := note.OrigID
			if id == "" {
				id = note.ID
			}
			if !noteKeys[id] {
				continue
			}
			keep(noteFromMeta(note))
		}
	}
	return decisionLineOf(found, toolHookDecisionBudget)
}

// decisionLineOf words what this file or command has standing, newest first.
//
// Two, when they fit. A file often has a broad rule and a narrow exception, and
// the exception is newer almost by definition — it is an exception to something
// — so showing only the newest handed the agent the half that reverses the
// rule, with nothing to say the rule existed (#2524). When the second does not
// fit, the line says one was left out rather than leaving the narrower half
// standing alone.
func decisionLineOf(notes []sources.PromotedNote, budget int) string {
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	var lines []string
	seen := map[string]bool{}
	for _, note := range notes {
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = strings.TrimSpace(note.Title)
		}
		if text == "" {
			continue
		}
		text = trimTrailingFragment(search.SafeText(clipDecision(text, budget)))
		if text == "" || seen[strings.ToLower(text)] {
			continue
		}
		seen[strings.ToLower(text)] = true
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	rest := len(lines) - 1
	if rest > 0 {
		if joined := out + "; also: " + lines[1]; len(joined) <= budget {
			out, rest = joined, rest-1
		}
	}
	if rest > 0 {
		out += fmt.Sprintf(" (+%d more standing here)", rest)
	}
	return out
}

// noteFromMeta reads the decision a session carries as a promoted note, so the
// two sources — this machine's notes file and what arrived by sync — can be
// weighed against each other by one rule.
func noteFromMeta(meta index.SessionMeta) sources.PromotedNote {
	when := meta.Updated
	if t, err := time.Parse(time.RFC3339, meta.LifecycleAt); err == nil {
		when = t
	}
	text := meta.LifecycleNote
	if strings.TrimSpace(text) == "" {
		text = meta.Title
	}
	return sources.PromotedNote{Project: meta.Project, State: meta.Lifecycle, Title: meta.Title, Text: text, At: when}
}

// clipDecision keeps a promoted note inside the same budget a scanned
// conclusion is held to, so one long note cannot take the line over.
func clipDecision(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	// On a rune boundary: a note in Russian or Japanese cut mid-character puts
	// invalid UTF-8 into the payload an agent is handed.
	end := budget
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}

// decisionUsable rejects a session whose decision should not be reused: one that
// says in its own words it backed the approach out (GaveUp), and one a later
// state marked rejected, superseded or stale. Surfacing either at the point of an
// edit would push the agent to redo exactly what was undone.
func decisionUsable(s model.Session, states map[string]sources.Lifecycle) bool {
	if s.GaveUp {
		return false
	}
	state := s.Lifecycle // a note that arrived by sync carries its own state
	if lc, ok := states[s.Harness+":"+s.ID]; ok && lc.State != "" {
		state = lc.State
	}
	switch state {
	case "rejected", "superseded", "stale":
		return false
	}
	return true
}

// trimTrailingFragment drops a half-word left by a byte-budget cut, so a decision
// reads as "capped retries" rather than "capped retr". Only when the text was
// actually cut (near the budget and not ending a sentence).
func trimTrailingFragment(s string) string {
	if len(s) < toolHookDecisionBudget-4 {
		return s
	}
	if r := s[len(s)-1]; r == '.' || r == '!' || r == '?' {
		return s
	}
	if i := strings.LastIndexAny(s, " \t"); i > 0 {
		return strings.TrimRight(s[:i], " \t") + "…"
	}
	return s
}

// fileMetaInScope keeps a session only if it worked on this exact path (an
// absolute path cannot collide across projects) or if it belongs to the
// project being worked in now. It is what stops a same-named file in an
// unrelated repo from being counted.
func fileMetaInScope(meta index.SessionMeta, path string, projects []string) bool {
	for _, t := range meta.Touched {
		if t == path {
			return true
		}
	}
	proj := strings.TrimPrefix(meta.Project, "imported:")
	for _, cand := range projects {
		if cand == "" {
			continue
		}
		// The shared rule, so this scope cannot drift from the one the session
		// start and the handoff use: a bare candidate is a suffix match only
		// for a peer's project, whose path is not this machine's. Taking it
		// for a local one answered an edit to /work/api/ledger.go with seven
		// sessions from a client's acme/api, and their decision (#2339).
		if index.ProjectInScope(meta.Project, cand) {
			return true
		}
		// The other direction: a store that records the bare project name
		// ("api") against a candidate that carries the parent ("work/api").
		if strings.HasSuffix(cand, "/"+proj) {
			return true
		}
	}
	return false
}

func baseName(p string) string { return index.CrossBase(p) }

// truncateToolLine caps the line at max bytes on a rune boundary, so a
// non-ASCII filename near the limit is not split mid-rune into a U+FFFD.
func truncateToolLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// shortHash fingerprints the injected line for per-session dedupe.
func shortHash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}

// toolSessionCount words a count for a line read inside an action.
func toolSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
