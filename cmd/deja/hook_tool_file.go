package main

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func fileHookLine(dir, cwd, path string) string {
	return fileHookLineOutside(dir, cwd, path, nil)
}

// fileHookLineOutside leaves out the sessions self reports: the one the hook
// fires in and what counts as it. It is in the index once it has touched the
// file, and as the newest it was counted and its own last reply quoted back as
// the file's history (#4380).
func fileHookLineOutside(dir, cwd, path string, self func(index.SessionMeta, string) bool) string {
	// FileSessions matches on the file's basename, so without scoping "main.go"
	// or "README.md" collects every project's file of that name — the line then
	// claims a history this file does not have and points `deja blame` at a
	// pile of other repos. Count only sessions in the project being worked in,
	// unless the stored path is the exact one (which cannot collide). Asked
	// for only when a session needs them: the names list the repository's
	// worktrees, which is a subprocess on every edit.
	projects := sync.OnceValue(func() []string {
		names := digest.ProjectNameCandidates(cwd)
		if filepath.IsAbs(path) && cwd != "" {
			cleanCwd := filepath.Clean(cwd)
			cleanPath := filepath.Clean(path)
			if strings.HasPrefix(cleanPath, cleanCwd+string(filepath.Separator)) {
				dirPath := filepath.Dir(cleanPath)
				for _, cand := range digest.ProjectNameCandidates(dirPath) {
					if !slices.Contains(names, cand) {
						names = append(names, cand)
					}
				}
				for cur := dirPath; strings.HasPrefix(cur, cleanCwd+string(filepath.Separator)); cur = filepath.Dir(cur) {
					base := filepath.Base(cur)
					parent := filepath.Base(filepath.Dir(cur))
					for _, cand := range []string{parent + "/" + base, base} {
						if cand != "" && !slices.Contains(names, cand) {
							names = append(names, cand)
						}
					}
				}
			}
		}
		return names
	})
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	var inScope []index.SessionMeta
	for _, meta := range index.FileSessions(dir, path) {
		if self != nil && self(meta, path) {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !fileMetaInScope(meta, path, nil) && !fileMetaInScope(meta, path, projects()) {
			continue
		}
		sessions++
		inScope = append(inScope, meta)
		if meta.Updated.After(last) {
			last = meta.Updated
		}
	}
	if sessions < toolHookMinFileSessionsWithDecision {
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
	// The line speaks only when what it carries is about this file: a decision
	// that names it, or a command two sessions here ran on it. Read from a month
	// of real Claude Code sessions, 114 file lines and not one the agent acted
	// on: no recall or blame that followed from one, no reply that used it. 56
	// of them were the same accepted note about the repository description,
	// reached through one long session that touched every file; the
	// session-start block already carries that note as a standing decision.
	// 37 were the closing sentence of
	// the newest session ("last session on it ended") and 8 the bare `deja
	// blame` pointer, which the A/B behind this hook already found changed
	// nothing. What the same A/B found an agent reuses is the decision about the
	// file, so that is what is left.
	var dec string
	if d := promotedDecisionForFile(path, inScope); d != "" && mentionsFile(path, d) {
		dec = standingLabel + d
	} else if d := fileDecisionLine(dir, path, inScope); d != "" && digest.CarriesDecision(d) && mentionsFile(path, d) {
		// A scanned line is a decision about the file only when it reads as one and
		// names the file. Of 63 lines once offered as "prior decision", none named
		// the file or its package.
		if promotedDecisionForFile(path, inScope) == d {
			dec = decisionLabel + d
		} else {
			dec = modelLabel(decisionLabel, d) + d
		}
	}
	ran := fileHookRanLine(dir, inScope)
	var body string
	switch {
	case dec != "" && strings.HasPrefix(dec, standingLabel):
		body = dec
		if ran != "" && len(head)+len(body)+len(ran) <= toolHookMaxBytes {
			body += ran
		}
	case ran != "":
		// A scanned conclusion does not outrank a verification command that two
		// sessions ran here and passed; when both fit, carry both.
		body = ran
		if dec != "" && len(head)+len(body)+len(dec) <= toolHookMaxBytes {
			body = dec + ran
		}
	default:
		body = dec
	}
	co := fileHookCoChangeLine(path, inScope)
	if body == "" {
		if co != "" {
			return head + co
		}
		return ""
	}
	out := head + body
	if co != "" && len(out)+len(co) <= toolHookMaxBytes {
		out += co
	}
	return out
}

const decisionLabel = " — prior decision: "

// mentionsFile reports whether the text names the file or the directory it sits
// in. Stems count: a decision about `render.go` says "renderer", and one about
// `notes.jsonl` says "notes".
func mentionsFile(path, text string) bool {
	low := strings.ToLower(text)
	base := strings.ToLower(filepath.Base(path))
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	parts = append(parts, strings.ToLower(filepath.Base(filepath.Dir(path))))
	for _, w := range parts {
		if len(w) < 4 {
			continue
		}
		if strings.Contains(low, w) {
			return true
		}
		// The plural or the agent noun: notes -> note, render -> renderer is
		// already covered by the containment above.
		if strings.HasSuffix(w, "s") && strings.Contains(low, strings.TrimSuffix(w, "s")) {
			return true
		}
	}
	return false
}

// standingLabel introduces a promoted note, and says what it is.
//
// A note is promoted by the user in a session, not against a file, so it is
// reached here through "a session that worked on this file said it" — which is
// not the same as "this was decided about this file". Read on a real store, the
// one accepted note on the machine arrived in front of `main.go` as "prior
// decision", where it is a rule about the repository description. The note is
// worth carrying and the ordering behind it was measured (#2495); what it must
// not do is claim to be about the file the agent is holding.
const standingLabel = " — standing decision in this project: "

// fileDecisionLine returns the single most relevant prior decision recorded
// about this file, or "" if none can be extracted. It reads the newest in-scope
// sessions (bounded, newest first) and takes the first that yields a conclusion
// and was not later taken back — the same decision-carrying line the SessionStart
// digest surfaces, but delivered at the moment the file is about to change.
func fileDecisionLine(dir, path string, metas []index.SessionMeta) string {
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	// A promoted note first. It is the user's own statement about this work,
	// which is why the session-start block leads with it and calls it standing;
	// scanning for a conclusion instead handed back whatever the newest session
	// happened to end with — "looked at retry.go again (5)" in front of "the
	// retry budget stays at 5" (#2495).
	if line := promotedDecisionForFile(path, metas); line != "" {
		return line
	}
	r := newLifecycleResolver(dir)
	for i, meta := range metas {
		if i >= toolHookDecisionScan {
			break
		}
		s, ok, err := index.FindByIdentity(dir, meta.Harness, meta.ID)
		if err != nil || !ok || !decisionUsable(s, r) {
			continue
		}
		// Bound the work inside a hook the agent is blocked on: a marathon
		// session's whole history is not worth scanning for one line, and a
		// decision states itself near the end anyway.
		if len(s.Messages) > toolHookMsgTail {
			s.Messages = s.Messages[len(s.Messages)-toolHookMsgTail:]
		}
		if path == "" {
			if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
				return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0])))
			}
			continue
		}
		for _, c := range digest.Conclusions(s, toolHookDecisionBudget*4, 6) {
			line := trimTrailingFragment(search.SafeText(strings.TrimSpace(c)))
			if line != "" && digest.CarriesDecision(line) && mentionsFile(path, line) {
				return line
			}
		}
	}
	return ""
}

// promotedDecisionFor returns the newest accepted promoted note belonging to one
// of these sessions. Accepted only, for the reason projectConventions gives: a
// decision later reversed carries a non-accepted state, and LoadPromotedNotes
// keeps the latest state per source.
func promotedDecisionFor(metas []index.SessionMeta) string {
	return promotedDecisionForFile("", metas)
}

func promotedDecisionForFile(path string, metas []index.SessionMeta) string {
	if len(metas) == 0 {
		return ""
	}
	want := make(map[string]bool, len(metas))
	noteKeys := make(map[string]bool, len(metas))
	for _, meta := range metas {
		want[meta.Key()] = true
		if meta.OrigID != "" {
			want[model.SessionKey(meta.Harness, meta.OrigID)] = true
		}
		for _, id := range meta.CandidatePromotedNoteIDs() {
			noteKeys[id] = true
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
	line := decisionLineOf(found, toolHookDecisionBudget)
	if path == "" || line == "" || mentionsFile(path, line) {
		return line
	}
	var matching []sources.PromotedNote
	for _, note := range found {
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = strings.TrimSpace(note.Title)
		}
		if mentionsFile(path, text) {
			matching = append(matching, note)
		}
	}
	return decisionLineOf(matching, toolHookDecisionBudget)
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
	text := meta.LifecycleNote
	if strings.TrimSpace(text) == "" {
		text = meta.Title
	}
	src, _ := meta.PromotedNoteSourceKey()
	return sources.PromotedNote{
		Project: meta.Project,
		Session: src,
		State:   meta.Lifecycle,
		Title:   meta.Title,
		Text:    text,
		At:      meta.LifecycleTime(),
	}
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
func decisionUsable(s model.Session, r *lifecycleResolver) bool {
	if s.GaveUp {
		return false
	}
	state, _, _, _, ok := r.resolve(s)
	if !ok {
		return true
	}
	switch state {
	case model.LifecycleRejected, model.LifecycleSuperseded, model.LifecycleStale:
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
	if slices.Contains(meta.Touched, path) {
		return true
	}
	proj := meta.BareProject()
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
func truncateToolLine(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
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

// toolCommandTerms is what the tool hook searches on for a command: its text
// with the shell noise taken off, read as a command rather than as prose.
func toolCommandTerms(cmd string) []string {
	return prompt.CommandTerms(normalizedCommandText(cmd))
}
