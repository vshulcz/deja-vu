package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// orient answers the question an agent otherwise answers by reading: how is
// work done in this repository. The other modes all wait for a question —
// recall needs one, fix needs an error, how needs a name to look up — so the
// first minutes of a session go on globbing for a Makefile and opening files
// to find out where the work lives, and that reading is the expensive part.
// Every file the agent opens stays in the prompt and is re-read on every later
// turn, so the cost of exploring is the length of what it found times the turns
// that follow it.
//
// What comes back is what past sessions in this project actually did: the
// commands they ran, ranked by how many sessions ran them, and the files they
// opened or edited. It is a map, not an instruction — the closing line says so,
// because a stale command run confidently is worse than no answer.
const (
	orientCommandsShown = 6
	orientFilesShown    = 8
	// orientPathMax keeps a generated or vendored path from filling a line.
	orientPathMax = 80
	// orientCommandMax is where a line stops being a command and starts being a
	// paragraph the reader skips.
	orientCommandMax = 72
	// What the session-start digest shows, which is less than the mode does:
	// it arrives in every session whether or not it is wanted.
	orientDigestCommands = 3
	orientDigestFiles    = 4
	orientDigestCoupled  = 2
	orientDigestFixes    = 2
)

func mcpOrient(dir, name string, raw json.RawMessage) (string, int, error) {
	setActiveDejaDir(dir)
	var a struct {
		Project   string    `json:"project"`
		Limit     mcpNumber `json:"limit"`
		SessionID string    `json:"session_id"`
		CWD       string    `json:"cwd"`
	}
	if err := decodeToolArgs(name, raw, &a); err != nil {
		return "", 0, err
	}
	if line := buildingNowForAgent(dir); line != "" {
		return line, 0, nil
	}
	if _, err := index.EnsureForSearchStale(dir, search.Options{}, mcpProgress()); err != nil {
		return "", 0, err
	}
	limit := int(a.Limit)
	if limit <= 0 {
		limit = orientCommandsShown
	}
	cwd := strings.TrimSpace(a.CWD)
	if cwd == "" {
		cwd = howCwd()
	}
	sid := strings.TrimSpace(a.SessionID)
	if sid == "" {
		sid = os.Getenv("DEJA_SESSION_ID")
		if sid == "" {
			sid = os.Getenv("SESSION_ID")
		}
	}
	scope := howScope(cwd, a.Project, false)
	cmds, hidden, ignored, err := howEntries(dir, nil, scope, policy.ActivationMCP)
	if err != nil {
		return "", 0, err
	}
	cmds = filterAndSortOrientCmds(cmds)
	if strings.TrimSpace(a.Project) != "" && !howProjectMatches(a.Project, howScope(cwd, "", false)) {
		cwd = ""
	}
	files := orientFiles(dir, scope, cwd)
	if cwd != "" {
		if root := sources.RepoRoot(cwd); root != "" && fileThere(sources.RepoMemoryPath(root)) {
			autoEvolveRepoMemory(dir, cwd)
		}
	}
	repoEntries := orientRepoMemory(cwd, policy.ActivationMCP)
	where := howScopeName(scope)
	if where == "" {
		where = "this machine"
	}
	var deferredText string
	if sid != "" || cwd != "" {
		deferredText = takeDeferred(dir, deferredKey(sid, cwd))
		if deferredText == "" && sid != "" && cwd != "" {
			deferredText = takeDeferred(dir, deferredKey("", cwd))
		}
	}
	var compBuf bytes.Buffer
	if sid != "" || cwd != "" {
		_, _ = emitCompactionRecovery(dir, sid, cwd, "SessionStart", hookToolPlain, &compBuf)
	}
	recoveryText := strings.TrimSpace(compBuf.String())

	if len(cmds) == 0 && len(files) == 0 && len(repoEntries) == 0 {
		if recoveryText != "" || deferredText != "" {
			var b strings.Builder
			if recoveryText != "" {
				b.WriteString(recoveryText + "\n\n")
			}
			if deferredText != "" {
				b.WriteString(deferredText + "\n\n")
			}
			return frameRecall(strings.TrimSpace(b.String())), 1, nil
		}
		if note := ignoredHiddenNoteFor("answer", ignored); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		if note := policyHiddenNote(policy.ActivationMCP, hidden); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		// Sessions with nothing to orient by are still sessions: saying none
		// worked here sent an agent away from history recall had.
		if n := orientSessionCount(dir, scope); n > 0 {
			return fmt.Sprintf("%s worked in %s, but no command or file repeats across them. Recall with project %q reads what they said.", pluralSessions(n), where, where), 0, nil
		}
		return fmt.Sprintf("No past session on this machine worked in %s.", where) + emptyStoreNote(dir), 0, nil
	}

	coupled := orientCoChangePairs(dir, scope, cwd, files, policy.ActivationMCP)
	fixes := orientProjectFixes(dir, scope, cwd, policy.ActivationMCP)
	corrections := orientProjectCorrections(dir, scope, policy.ActivationMCP)
	text, count := renderOrientSections(where, recoveryText, deferredText, repoEntries, cmds, files, limit, coupled, fixes, corrections)
	return text, count, nil
}

func renderOrientSections(where, recoveryText, deferredText string, repoEntries []sources.RepoEntry, cmds []howEntry, files []orientFile, limit int, coupled, fixes, corrections []string) (string, int) {
	var b strings.Builder
	if recoveryText != "" {
		b.WriteString(recoveryText + "\n\n")
	}
	if deferredText != "" {
		b.WriteString(deferredText + "\n\n")
	}
	if len(cmds) > 0 || len(files) > 0 {
		fmt.Fprintf(&b, "how work has been done in %s, from past sessions here\n", where)
	} else {
		fmt.Fprintf(&b, "from this repository's %s (committed by the people working on it):\n", repoFileShown)
	}
	if len(repoEntries) > 0 {
		if len(cmds) > 0 || len(files) > 0 {
			fmt.Fprintf(&b, "committed in %s:\n", repoFileShown)
		}
		for i, e := range repoEntries {
			if i >= orientFilesShown {
				break
			}
			fmt.Fprintf(&b, "- [%s] %s\n", e.Kind, search.SafeLine(e.Text))
		}
	}
	if len(cmds) > 0 {
		fmt.Fprintln(&b, "commands, by how many sessions ran them:")
		for i, e := range cmds {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "- %s · %s", orientCommand(e.Command), pluralSessions(len(e.Sessions)))
			if e.Failures > 0 {
				fmt.Fprintf(&b, " · failed in %d", e.Failures)
			}
			if !e.Last.IsZero() {
				fmt.Fprintf(&b, " · last %s", e.Last.Format("Jan 2"))
			}
			fmt.Fprintln(&b)
		}
	}
	if len(files) > 0 {
		fmt.Fprintln(&b, "files those sessions opened or edited:")
		for i, f := range files {
			if i >= orientFilesShown {
				break
			}
			fmt.Fprintf(&b, "- %s · %s\n", f.Path, pluralSessions(f.Sessions))
		}
	}
	if len(coupled) > 0 {
		fmt.Fprintln(&b, "coupled files (usually edited together):")
		for _, pair := range coupled {
			fmt.Fprintf(&b, "- %s\n", pair)
		}
	}
	if len(fixes) > 0 {
		fmt.Fprintln(&b, "known traps and verified fixes here:")
		for i, fx := range fixes {
			if i >= orientCommandsShown {
				break
			}
			fmt.Fprintf(&b, "- %s\n", fx)
		}
	}
	if len(corrections) > 0 {
		fmt.Fprintln(&b, "recent user corrections here (do not repeat):")
		for i, c := range corrections {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(cmds) > 0 || len(files) > 0 {
		fmt.Fprint(&b, "This is what was done here before, not a rule — check a command still fits before running it.")
	}
	return frameRecall(strings.TrimSpace(b.String())), len(repoEntries) + len(cmds) + len(files) + len(coupled) + len(fixes) + len(corrections)
}

func orientRepoMemory(cwd, act string) []sources.RepoEntry {
	if cwd == "" {
		return nil
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		root = cwd
	}
	if !policy.Load().Allows(act, sources.RepoMemoryProject(root)) {
		return nil
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return nil
	}
	sources.RememberRepoCheckout(root)
	rank := map[string]int{"rule": 0, "rejected": 1, "gotcha": 2, "decision": 3}
	sort.SliceStable(entries, func(i, j int) bool {
		ri, rj := rank[entries[i].Kind], rank[entries[j].Kind]
		if ri != rj {
			return ri < rj
		}
		return entries[i].Date > entries[j].Date
	})
	return entries
}

const orientRecentCommandWindow = 30 * 24 * time.Hour

func orientRecentCommand(last, newest time.Time) bool {
	if newest.IsZero() || last.IsZero() {
		return true
	}
	return newest.Sub(last) <= orientRecentCommandWindow
}

func filterAndSortOrientCmds(cmds []howEntry) []howEntry {
	filteredCmds := cmds[:0]
	var newestLast time.Time
	for _, e := range cmds {
		if !orientEphemeralCommand(orientCommand(e.Command)) {
			filteredCmds = append(filteredCmds, e)
			if e.Last.After(newestLast) {
				newestLast = e.Last
			}
		}
	}
	cmds = filteredCmds
	sort.SliceStable(cmds, func(i, j int) bool {
		ci, cj := orientCommand(cmds[i].Command), orientCommand(cmds[j].Command)
		si, sj := orientSuiteVerifyCommand(ci), orientSuiteVerifyCommand(cj)
		if si != sj {
			return si
		}
		vi, vj := index.VerifyCommand(ci), index.VerifyCommand(cj)
		if vi != vj {
			return vi
		}
		ii, ij := index.InspectionCommand(ci), index.InspectionCommand(cj)
		if ii != ij {
			return !ii
		}
		ri, rj := orientRecentCommand(cmds[i].Last, newestLast), orientRecentCommand(cmds[j].Last, newestLast)
		if ri != rj {
			return ri
		}
		if len(cmds[i].Sessions) != len(cmds[j].Sessions) {
			return len(cmds[i].Sessions) > len(cmds[j].Sessions)
		}
		return cmds[i].Last.After(cmds[j].Last)
	})
	seenCmd := map[string]bool{}
	dedupCmds := cmds[:0]
	for _, e := range cmds {
		clean := orientCommand(e.Command)
		if seenCmd[clean] {
			continue
		}
		seenCmd[clean] = true
		dedupCmds = append(dedupCmds, e)
	}
	return dedupCmds
}

// orientCommand is the command without the way one machine reached it. Half
// these lines start "cd /Users/<name>/…; " because that is how an agent runs a
// command from somewhere else, and the prefix is both the longest part of the
// line and the part that means nothing to the reader.
func orientCommand(cmd string) string {
	cmd = strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")
	for {
		low := strings.ToLower(cmd)
		if !strings.HasPrefix(low, "cd ") {
			break
		}
		i := strings.IndexAny(cmd, ";&")
		if i < 0 || i+1 >= len(cmd) {
			break
		}
		cmd = strings.TrimSpace(strings.TrimLeft(cmd[i+1:], "&; "))
	}
	cmd = stripViewerPipe(cmd)
	if len(cmd) > orientCommandMax {
		cmd = strings.TrimSpace(truncateToolLine(cmd, orientCommandMax)) + "…"
	}
	return cmd
}

// orientPath is the path as the reader's editor would name it: relative to the
// directory the question was asked from, when it is under it. Outside it the
// path is not this project's map at all — the top two entries on the machine
// this was written on were the user's own memory files, edited from every
// project — so the caller drops what comes back unchanged.
func orientPath(p, cwd string) string {
	p = stripWorktreeSegment(p)
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if mainRoot := orientMainRepoRoot(cwd); mainRoot != "" {
		if rel, err := filepath.Rel(mainRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if rel := resolveForeignWorktreeSuffix(p, cwd); rel != "" {
		return rel
	}
	return p
}

func resolveForeignWorktreeSuffix(p, cwd string) string {
	if cwd == "" || !filepath.IsAbs(p) {
		return ""
	}
	slash := filepath.ToSlash(p)
	low := strings.ToLower(slash)
	if !strings.HasPrefix(low, "/tmp/") && !strings.HasPrefix(low, "/private/tmp/") && !strings.Contains(low, "/worktrees/") {
		return ""
	}
	tree := orientRepoTree(cwd)
	parts := strings.Split(strings.TrimPrefix(slash, "/"), "/")
	for i := 1; i < len(parts)-1; i++ {
		rel := strings.Join(parts[i:], "/")
		if !strings.Contains(rel, "/") {
			continue
		}
		if tree.rel[rel] {
			return filepath.FromSlash(rel)
		}
	}
	return ""
}

func stripWorktreeSegment(p string) string {
	slash := filepath.ToSlash(p)
	for _, marker := range []string{"/.claude/worktrees/", "/.git/worktrees/"} {
		if before, rest, ok := strings.Cut(slash, marker); ok {
			if _, after, ok := strings.Cut(rest, "/"); ok {
				return filepath.FromSlash(before + "/" + after)
			}
		}
	}
	return p
}

func orientMainRepoRoot(cwd string) string {
	gitPath := filepath.Join(cwd, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil || fi.IsDir() {
		return ""
	}
	raw, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	after, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitDir := filepath.ToSlash(strings.TrimSpace(after))
	if idx := strings.Index(gitDir, "/.git/worktrees/"); idx > 0 {
		return filepath.FromSlash(gitDir[:idx])
	}
	return ""
}

// orientFile is one path and how many sessions in the scope touched it.
type orientFile struct {
	Path     string
	Sessions int
	Last     time.Time
}

// orientSessionCount is how many sessions the trust policy shows in scope.
func orientSessionCount(dir string, projects []string) int {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0
	}
	pol := policy.Load()
	n := 0
	for _, m := range metas {
		if howProjectMatches(m.Project, projects) && metaVisible(pol, policy.ActivationMCP, m) {
			n++
		}
	}
	return n
}

// orientFiles ranks what this project's sessions worked on, from the manifest
// rather than the record log. The manifest already keeps the few files each
// session worked on most (SessionMeta.Touched, #3605 put it there for the
// point-of-action hook), and it is read from a cache; walking the records for
// the same answer cost a full pass over a 125 MB log on a surface that fires at
// every session start.
func orientFiles(dir string, projects []string, cwd string) []orientFile {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	type acc struct {
		sessions int
		last     time.Time
	}
	byPath := map[string]*acc{}
	for _, m := range metas {
		if !howProjectMatches(m.Project, projects) {
			continue
		}
		if !metaVisible(pol, policy.ActivationMCP, m) {
			continue
		}
		seenInSession := map[string]bool{}
		for _, p := range m.Touched {
			p = strings.TrimSpace(p)
			if p == "" || isOrientEphemeralFile(p) {
				continue
			}
			named := orientPath(p, cwd)
			if len(named) > orientPathMax {
				continue
			}
			// A path the cwd could not shorten is outside this project.
			if cwd != "" && named == p && filepath.IsAbs(p) {
				continue
			}
			if seenInSession[named] {
				continue
			}
			seenInSession[named] = true
			a := byPath[named]
			if a == nil {
				a = &acc{}
				byPath[named] = a
			}
			a.sessions++
			if m.Updated.After(a.last) {
				a.last = m.Updated
			}
		}
	}
	tree := orientRepoTree(cwd)
	checkDisk := orientMatchesAny(tree, byPath)
	out := make([]orientFile, 0, len(byPath))
	for named, a := range byPath {
		// One session opening a file says nothing about the project; the point
		// of the list is where work keeps landing.
		if a.sessions < 2 {
			continue
		}
		if checkDisk && !tree.exists(named) {
			continue
		}
		out = append(out, orientFile{Path: named, Sessions: a.sessions, Last: a.last})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// orientDigestBlock is the same map, shortened for the session-start digest.
// The mode is only reached when a model decides to call it, and measured over
// six runs on a 325-file repository it decided to five times out of six — it
// reached for recall with the task's own words instead, which answers what was
// said and not where the work is. The map is cheap enough to arrive unasked:
// three commands and four files, against the five to ten file reads that open
// a session in a repository the agent has not seen.
//
// Empty when the project has nothing recurring, because a map of one session's
// keystrokes is noise sitting in every prompt of every session after it.
func orientDigestBlock(dir, cwd string, projects []string, activation string) string { //nolint:unparam // forwarded to orientRecurringCommands
	if cwd == "" || (!filepath.IsAbs(cwd) && howProjectMatches(cwd, howScope(howCwd(), "", false))) {
		// The hook is handed the project by the harness and not always the
		// directory; without one every path stays absolute and the list fills
		// with files from outside the project.
		cwd = howCwd()
	}
	recurring := orientRecurringCommands(dir, projects, activation)
	files := orientFiles(dir, projects, cwd)
	if len(recurring) == 0 && len(files) == 0 {
		return ""
	}
	coupled := orientFilterCoupledByRepoMemory(cwd, orientCoChangePairs(dir, projects, cwd, files, activation))
	fixes := orientProjectFixes(dir, projects, cwd, activation)
	corrections := orientProjectCorrections(dir, projects, activation)
	var b strings.Builder
	b.WriteString("What work looks like in this project, from its past sessions:\n")
	for i, c := range recurring {
		if i >= orientDigestCommands {
			break
		}
		fmt.Fprintf(&b, "- %s · %s\n", orientCommand(c.Text), pluralSessions(c.Sessions))
	}
	if len(files) > 0 {
		var shown []string
		for i, f := range files {
			if i >= orientDigestFiles {
				break
			}
			shown = append(shown, f.Path)
		}
		fmt.Fprintf(&b, "- the files its sessions keep opening: %s\n", strings.Join(shown, ", "))
	}
	if len(coupled) > 0 {
		var shown []string
		for i, pair := range coupled {
			if i >= orientDigestCoupled {
				break
			}
			shown = append(shown, pair)
		}
		fmt.Fprintf(&b, "- coupled files (usually edited together): %s\n", strings.Join(shown, "; "))
	}
	for i, fx := range fixes {
		if i >= orientDigestFixes {
			break
		}
		fmt.Fprintf(&b, "- known trap here: %s\n", fx)
	}
	for i, corr := range corrections {
		if i >= 2 {
			break
		}
		fmt.Fprintf(&b, "- user correction here: %s\n", corr)
	}
	b.WriteString("Check a command still fits before running it.\n")
	return b.String()
}

// orientCommandUse is one recurring command and how many of this project's
// sessions ran it.
type orientCommandUse struct {
	Text        string
	Sessions    int
	Last        time.Time
	SuiteVerify bool
	Verify      bool
	Inspect     bool
}

// orientRecurringCommands reads the table the build already writes, not the
// record log. `deja how` scans the log because it answers a question someone
// typed and can spend the seconds; this block goes out at session start, where
// the rule is that the hook stays in milliseconds.
//
// Two sessions is what makes a command this project's practice rather than one
// session's typing — the same bar the table itself applies before keeping a
// command at all.
func orientRecurringCommands(dir string, projects []string, activation string) []orientCommandUse {
	pol := policy.Load()
	var out []orientCommandUse
	for _, cu := range index.ReadCommands(dir) {
		n := 0
		var last time.Time
		for proj, use := range cu.ByProject {
			if !howProjectMatches(proj, projects) || !pol.Allows(activation, proj) {
				continue
			}
			n += use.Sessions
			if use.Last.After(last) {
				last = use.Last
			}
		}
		if n < 2 {
			continue
		}
		clean := orientCommand(cu.Command)
		if orientEphemeralCommand(clean) {
			continue
		}
		out = append(out, orientCommandUse{
			Text:        cu.Command,
			Sessions:    n,
			Last:        last,
			SuiteVerify: orientSuiteVerifyCommand(clean),
			Verify:      index.VerifyCommand(clean),
			Inspect:     index.InspectionCommand(clean),
		})
	}
	var newestLast time.Time
	for _, c := range out {
		if c.Last.After(newestLast) {
			newestLast = c.Last
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SuiteVerify != out[j].SuiteVerify {
			return out[i].SuiteVerify
		}
		if out[i].Verify != out[j].Verify {
			return out[i].Verify
		}
		if out[i].Inspect != out[j].Inspect {
			return !out[i].Inspect
		}
		ri, rj := orientRecentCommand(out[i].Last, newestLast), orientRecentCommand(out[j].Last, newestLast)
		if ri != rj {
			return ri
		}
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Text < out[j].Text
	})
	seenClean := map[string]bool{}
	dedup := out[:0]
	for _, c := range out {
		clean := orientCommand(c.Text)
		if seenClean[clean] {
			continue
		}
		seenClean[clean] = true
		dedup = append(dedup, c)
	}
	return dedup
}

// orientCoChangePairs finds high-confidence file pairs in this project that
// sessions almost always edited together (>= 3 focused sessions and >= 2/3
// co-change ratio), seeding from top hot files in the manifest cache.
func orientCoChangePairs(dir string, projects []string, cwd string, files []orientFile, activation string) []string {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	if cwd == "" {
		cwd = howCwd()
	}
	if len(projects) > 0 && !howProjectMatches(projects[0], howScope(cwd, "", false)) && orientMainRepoRoot(cwd) == "" {
		cwd = ""
	}
	tree := orientRepoTree(cwd)
	var inScope []index.SessionMeta
	hasOnDisk := false
	for _, m := range metas {
		if howProjectMatches(m.Project, projects) && metaVisible(pol, activation, m) {
			inScope = append(inScope, m)
			if !hasOnDisk && len(tree.base) > 0 {
				for _, t := range m.Touched {
					if tree.exists(orientPath(t, cwd)) {
						hasOnDisk = true
						break
					}
				}
			}
		}
	}
	if len(inScope) < toolHookMinCoChangeSessions {
		return nil
	}
	if hasOnDisk {
		filteredScope := make([]index.SessionMeta, 0, len(inScope))
		for _, m := range inScope {
			var kept []string
			for _, t := range m.Touched {
				named := orientPath(t, cwd)
				if !isOrientEphemeralFile(t) && tree.exists(named) {
					kept = append(kept, t)
				}
			}
			m.Touched = kept
			filteredScope = append(filteredScope, m)
		}
		inScope = filteredScope
	}
	var candidates []string
	seenTarget := map[string]bool{}
	addCandidate := func(p string) {
		if isOrientEphemeralFile(p) {
			return
		}
		b := strings.ToLower(baseName(p))
		if b == "" || seenTarget[b] {
			return
		}
		seenTarget[b] = true
		candidates = append(candidates, p)
	}
	for i, f := range files {
		if i >= 32 {
			break
		}
		addCandidate(f.Path)
	}
	if len(candidates) == 0 {
		for _, m := range inScope {
			for _, t := range m.Touched {
				if !isScratch(t) && !isOrientEphemeralFile(t) {
					addCandidate(t)
				}
			}
		}
	}
	seenPair := map[string]bool{}
	var out []string
	for _, target := range candidates {
		targetBase := strings.ToLower(baseName(target))
		var touching []index.SessionMeta
		for _, m := range inScope {
			for _, t := range m.Touched {
				if strings.EqualFold(baseName(t), targetBase) {
					touching = append(touching, m)
					break
				}
			}
		}
		companion, both, focused := coChangeCompanionIn(cwd, target, touching)
		if companion == "" {
			continue
		}
		a, b := targetBase, strings.ToLower(companion)
		if a > b {
			a, b = b, a
		}
		pairKey := a + "\x00" + b
		if seenPair[pairKey] {
			continue
		}
		seenPair[pairKey] = true
		out = append(out, fmt.Sprintf("%s ↔ %s (%d of %d sessions)",
			search.SafePath(baseName(target)), companion, both, focused))
		if len(out) >= orientCommandsShown {
			break
		}
	}
	return out
}

func orientFilterCoupledByRepoMemory(cwd string, coupled []string) []string {
	if cwd == "" || len(coupled) == 0 {
		return coupled
	}
	root := sources.RepoRoot(cwd)
	if root == "" {
		return coupled
	}
	entries, _, err := readRepoMemoryAndNotes(root)
	if err != nil || len(entries) == 0 {
		return coupled
	}
	var out []string
	for _, pair := range coupled {
		sides, _, _ := strings.Cut(pair, " (")
		a, b, ok := strings.Cut(sides, " ↔ ")
		if !ok {
			out = append(out, pair)
			continue
		}
		aBase := strings.ToLower(baseName(strings.TrimSpace(a)))
		bBase := strings.ToLower(baseName(strings.TrimSpace(b)))
		covered := false
		for _, e := range entries {
			low := strings.ToLower(e.Text)
			if (strings.Contains(low, "requires updating") || strings.Contains(low, "edited alongside")) &&
				strings.Contains(low, aBase) && strings.Contains(low, bBase) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, pair)
		}
	}
	return out
}

// orientProjectFixes returns confirmed (non-candidate) error->fix pairs mined
// from past sessions in this project, newest first.
func orientProjectFixes(dir string, projects []string, cwd, activation string) []string {
	pol := policy.Load()
	var pairs []index.FixPair
	for _, p := range index.ReadFixes(dir) {
		if p.Candidate {
			continue
		}
		proj := strings.TrimSpace(p.Project)
		if proj == "" || !howProjectMatches(proj, projects) || !pol.Allows(activation, proj) {
			continue
		}
		pairs = append(pairs, p)
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		if !pairs[i].When.Equal(pairs[j].When) {
			return pairs[i].When.After(pairs[j].When)
		}
		return pairs[i].Error < pairs[j].Error
	})
	seen := map[string]bool{}
	var out []string
	for _, p := range pairs {
		rawErr := strings.TrimSpace(search.SafeLine(p.Error))
		if orientSkipFixError(rawErr) {
			continue
		}
		errText := truncateToolLine(rawErr, 60)
		if errText == "" {
			continue
		}
		cmd, ok := withoutFailedExit(strings.TrimPrefix(strings.TrimSpace(search.SafeText(p.Command)), "$ "))
		edit := strings.TrimSpace(search.SafeLine(p.Edit))
		var remedy string
		switch {
		case ok && cmd != "":
			cleanCmd := orientCommand(cmd)
			if index.InspectionCommand(cleanCmd) || orientEphemeralCommand(cleanCmd) || orientDestructiveRemedy(cleanCmd) || !orientTestRemedyMatchesFail(rawErr, cleanCmd) {
				continue
			}
			remedy = "`" + cleanCmd + "`"
		case edit != "":
			if isScratch(edit) || isOrientEphemeralFile(edit) {
				continue
			}
			relEdit := search.SafePath(orientPath(edit, cwd))
			if cwd != "" && filepath.IsAbs(relEdit) {
				continue
			}
			if orientTautologicalEdit(rawErr, relEdit) {
				continue
			}
			remedy = "edit " + relEdit
			if p.Symbol != "" {
				remedy += " (" + search.SafeLine(p.Symbol) + ")"
			}
			if p.Why != "" {
				remedy += " — " + truncateToolLine(search.SafeLine(p.Why), 100)
			}
		default:
			continue
		}
		if seen[errText] {
			continue
		}
		seen[errText] = true
		out = append(out, "`"+errText+"` → "+remedy)
	}
	return out
}

func orientProjectCorrections(dir string, projects []string, activation string) []string {
	if len(projects) == 0 {
		return nil
	}
	metas, err := index.AllMeta(dir)
	if err != nil || len(metas) == 0 {
		return nil
	}
	pol := policy.Load()
	var cands []index.SessionMeta
	for _, m := range metas {
		if !howProjectMatches(m.Project, projects) || !metaVisible(pol, activation, m) {
			continue
		}
		if m.Harness == "deja" || m.IsSpawned() || sources.IsSubagentPath(m.Path) {
			continue
		}
		if throwawayPath(m.Path) || throwawayPath(m.Project) {
			continue
		}
		cands = append(cands, m)
	}
	sort.Slice(cands, func(i, j int) bool { return index.NewestFirstMeta(cands[i], cands[j]) })
	if len(cands) > 8 {
		cands = cands[:8]
	}
	ids := make([]index.Identity, len(cands))
	for i, m := range cands {
		ids[i] = index.Identity{Harness: m.Harness, ID: m.ID}
	}
	roles := map[string]bool{"user": true, index.RoleStubOthers: true}
	ss, err := index.FindManyByIdentityInRolesTail(dir, ids, roles, 1500)
	if err != nil || len(ss) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		for i := len(s.Messages) - 1; i >= 1; i-- {
			if s.Messages[i].Role != "user" {
				continue
			}
			prevAgent := false
			for j := i - 1; j >= 0; j-- {
				if s.Messages[j].Role == "user" {
					break
				}
				if s.Messages[j].Role != "user" {
					prevAgent = true
					break
				}
			}
			if !prevAgent {
				continue
			}
			raw := strings.TrimSpace(s.Messages[i].Text)
			if !isOrientActionableCorrection(raw) {
				continue
			}
			line := search.SafeLine(squeezeTrim(raw, 140))
			key := strings.ToLower(line)
			if line == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, line)
			if len(out) >= 3 {
				return out
			}
		}
	}
	return out
}

func isOrientActionableCorrection(s string) bool {
	if s == "" || strings.HasPrefix(s, "!") || pseudoUserRe.MatchString(s) || !isStandingCorrection(s) {
		return false
	}
	if strings.Contains(s, "<SYSTEM_MESSAGE>") || strings.Contains(s, "<ADDITIONAL_METADATA>") ||
		strings.Contains(s, "<USER_REQUEST>") || strings.Contains(s, "not actually sent by the user") ||
		strings.ContainsRune(s, '?') {
		return false
	}
	return true
}

func orientSuiteVerifyCommand(cmd string) bool {
	if !index.VerifyCommand(cmd) {
		return false
	}
	low := " " + strings.ToLower(cmd) + " "
	return !strings.Contains(low, " -run ") && !strings.Contains(low, " -run=") && !strings.Contains(low, " -k ")
}

func orientEphemeralCommand(cmd string) bool {
	low := strings.ToLower(strings.TrimSpace(cmd))
	if low == "" {
		return true
	}
	for _, marker := range []string{
		"/private/tmp/", "/tmp/", "/var/folders/",
		".claude/worktrees/", ".git/worktrees/",
		"zz_probe", "zz_hunt", "origin/hunt/",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func isOrientEphemeralFile(p string) bool {
	slash := filepath.ToSlash(strings.TrimSpace(p))
	low := strings.ToLower(slash)
	base := strings.ToLower(baseName(slash))
	if strings.HasPrefix(base, "zz_") || strings.HasSuffix(base, "_tmp_test.go") || strings.HasSuffix(base, "_tmp.go") {
		return true
	}
	for _, marker := range []string{
		"/private/tmp/", "/tmp/", "/var/folders/",
		"/.git/", "/.deja/", "/deja-vu-loop/", "/.gemini/", "/.claude/projects/",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

type repoFileTree struct {
	rel    map[string]bool
	base   map[string]bool
	byBase map[string]string
	at     time.Time
}

var orientTreeCache sync.Map

func orientRepoTree(cwd string) repoFileTree {
	if cwd == "" {
		return repoFileTree{}
	}
	if v, ok := orientTreeCache.Load(cwd); ok {
		if cached, ok := v.(repoFileTree); ok && time.Since(cached.at) < 250*time.Millisecond {
			return cached
		}
	}
	t := repoFileTree{
		rel:    map[string]bool{},
		base:   map[string]bool{},
		byBase: map[string]string{},
		at:     time.Now(),
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return t
	}
	count := 0
	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // unreadable subdirectories are skipped during best-effort walk
		}
		if d.IsDir() {
			name := d.Name()
			if path != cwd && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "build" || name == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count > 8000 {
			return fs.SkipAll
		}
		b := strings.ToLower(d.Name())
		t.base[b] = true
		if rel, err := filepath.Rel(cwd, path); err == nil {
			slash := filepath.ToSlash(rel)
			t.rel[slash] = true
			if prev, exists := t.byBase[b]; !exists || len(slash) < len(prev) {
				t.byBase[b] = slash
			}
		}
		return nil
	})
	orientTreeCache.Store(cwd, t)
	return t
}

func (t repoFileTree) exists(named string) bool {
	if named == "" {
		return false
	}
	slash := filepath.ToSlash(named)
	if t.rel[slash] {
		return true
	}
	if !strings.Contains(slash, "/") {
		return t.base[strings.ToLower(slash)]
	}
	return false
}

func (t repoFileTree) resolveRel(named string) string {
	if named == "" {
		return ""
	}
	slash := strings.TrimPrefix(filepath.ToSlash(named), "./")
	if t.rel[slash] {
		return slash
	}
	if !strings.Contains(slash, "/") {
		return t.byBase[strings.ToLower(slash)]
	}
	return ""
}

func orientMatchesAny[V any](t repoFileTree, byPath map[string]V) bool {
	if len(t.base) == 0 {
		return false
	}
	for p := range byPath {
		if t.exists(p) {
			return true
		}
	}
	return false
}

func orientSkipFixError(errLine string) bool {
	low := strings.ToLower(strings.TrimSpace(errLine))
	if low == "" || strings.HasPrefix(low, "--- skip:") || strings.Contains(low, "[command timed out") ||
		strings.HasPrefix(low, "encountered error in step execution:") || strings.HasPrefix(low, "fatal: bad revision ") {
		return true
	}
	if strings.HasPrefix(low, "grep ") || strings.HasPrefix(low, "rg ") || strings.HasPrefix(low, "git diff") ||
		strings.HasPrefix(low, "git status") || strings.HasPrefix(low, "ls ") || strings.HasPrefix(low, "which ") {
		return true
	}
	if strings.HasPrefix(low, "fail\t") && strings.HasSuffix(low, "[build failed]") {
		return true
	}
	if strings.HasPrefix(low, "fail ") && strings.HasSuffix(low, "[build failed]") {
		return true
	}
	for _, compileNoise := range []string{
		": undefined: ", "imported and not used", "declared and not used",
		"syntax error:", "too many errors",
		"failed to push some refs", "protected branch", "gh006:",
	} {
		if strings.Contains(low, compileNoise) {
			return true
		}
	}
	return false
}

func orientDestructiveRemedy(cmd string) bool {
	low := " " + strings.ToLower(cmd) + " "
	for _, bad := range []string{
		" git reset ", " git checkout ", " git clean ", " git stash ",
		" git push ", " rm -rf ", " rm -f ",
		" npm view ", " gh pr ", " gh issue ",
		" --abort ",
	} {
		if strings.Contains(low, bad) {
			return true
		}
	}
	return false
}

func orientTestRemedyMatchesFail(errLine, cmd string) bool {
	if !strings.HasPrefix(strings.TrimSpace(errLine), "--- FAIL:") {
		return true
	}
	if index.VerifyCommand(cmd) {
		return false
	}
	lowCmd := strings.ToLower(strings.TrimSpace(cmd))
	for _, runner := range []string{"go test", "pytest", "cargo test", "npm test", "vitest"} {
		if strings.Contains(lowCmd, runner) {
			return false
		}
	}
	return true
}

func orientTautologicalEdit(errLine, relEdit string) bool {
	if relEdit == "" {
		return false
	}
	lowErr := strings.ToLower(filepath.ToSlash(errLine))
	lowEdit := strings.ToLower(filepath.ToSlash(relEdit))
	base := filepath.Base(lowEdit)
	return strings.HasPrefix(lowErr, lowEdit+":") || (len(base) >= 5 && strings.Contains(lowErr, base+":"))
}

// projectXRayLine summarizes what deja already knows about the current
// working repository during `deja install`, or "" when the directory has no
// indexed project practice yet.
func projectXRayLine(dir, cwd string) string {
	if !index.HasManifest(dir) {
		return ""
	}
	if cwd == "" {
		cwd = howCwd()
	}
	scope := howScope(cwd, "", false)
	if len(scope) == 0 {
		return ""
	}
	sessions := orientSessionCount(dir, scope)
	if sessions < 2 {
		return ""
	}
	where := howScopeName(scope)
	if where == "" {
		return ""
	}
	recurring := orientRecurringCommands(dir, scope, policy.ActivationSearch)
	files := orientFiles(dir, scope, cwd)
	coupled := orientCoChangePairs(dir, scope, cwd, files, policy.ActivationSearch)
	fixes := orientProjectFixes(dir, scope, cwd, policy.ActivationSearch)
	if len(recurring) == 0 && len(files) == 0 && len(coupled) == 0 && len(fixes) == 0 {
		return ""
	}
	parts := []string{pluralSessions(sessions)}
	if len(recurring) > 0 {
		parts = append(parts, "check: `"+orientCommand(recurring[0].Text)+"`")
	}
	if len(files) > 0 {
		var hot []string
		for i, f := range files {
			if i >= 2 {
				break
			}
			hot = append(hot, f.Path)
		}
		parts = append(parts, "hot: "+strings.Join(hot, ", "))
	}
	if len(coupled) > 0 {
		parts = append(parts, "coupled: "+coupled[0])
	}
	if len(fixes) > 0 {
		noun := "error→fix pairs"
		if len(fixes) == 1 {
			noun = "error→fix pair"
		}
		parts = append(parts, fmt.Sprintf("%d %s ready", len(fixes), noun))
	}
	return fmt.Sprintf("this repo (%s): %s", search.SafeLine(where), strings.Join(parts, " · "))
}
