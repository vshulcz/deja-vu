// Package digest builds the shareable/handoff text slices of a session:
// noise-filtered problem statements, conclusions, and the live tail.
package digest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/gitread"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// ShareBudget is the byte budget Share uses when given none.
const ShareBudget = 6 * 1024

// Share renders a session as text to paste elsewhere, within budget bytes.
func Share(s model.Session, budget int) string {
	if budget <= 0 {
		budget = ShareBudget
	}
	var b strings.Builder
	date := "unknown"
	if !s.Updated.IsZero() {
		date = s.Updated.Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "# deja share: %s\n\n", oneLine(s.ID))
	fmt.Fprintf(&b, "- Project: %s\n", oneLine(s.Project))
	fmt.Fprintf(&b, "- Harness: %s\n", s.Harness)
	fmt.Fprintf(&b, "- Date: %s\n\n", date)
	// appendSectionWithin is appendSection held to a smaller ceiling, so a
	// section that could fill the block leaves room for the one after it.
	appendSectionWithin := func(title string, messages []model.Message, ceiling int) {
		if len(messages) == 0 || b.Len() >= ceiling {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, m := range messages {
			if b.Len() >= ceiling {
				break
			}
			text := MessageText(m.Text)
			if text == "" {
				continue
			}
			chunk := text + "\n\n"
			// At a message boundary, with no cut marker: the marker promises
			// that nothing follows it, and something does — the section this
			// ceiling exists to leave room for. The block's closing sentence
			// already tells the reader it is a compact slice.
			if b.Len()+len(chunk) > ceiling {
				break
			}
			b.WriteString(chunk)
		}
	}
	appendSection := func(title string, messages []model.Message) {
		if len(messages) == 0 || b.Len() >= budget {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, m := range messages {
			if b.Len() >= budget {
				break
			}
			text := MessageText(m.Text)
			if text == "" {
				continue
			}
			chunk := text + "\n\n"
			cut := b.Len()+len(chunk) > budget
			if cut {
				chunk = cutMarked(chunk, budget-b.Len())
			}
			b.WriteString(chunk)
			// A marker says the passage before it was cut. Anything written
			// after it reads as continuing text and makes the marker a lie, and
			// trimming can leave enough room for another message to fit.
			if cut {
				break
			}
		}
	}
	var users, assistants []model.Message
	for _, m := range s.Messages {
		if noisyMessage(m.Text) || IsAgentArtifact(m.Text) {
			continue
		}
		switch m.Role {
		case "user":
			users = append(users, m)
		case "assistant":
			assistants = append(assistants, m)
		}
	}
	// Half the budget for the question, half for the answer. The sections were
	// written in order until the budget ran out, so a long session — where the
	// user side is mostly "go on" — spent the whole block on problem
	// statements and handed over no conclusions at all, which is the half a
	// receiving agent cannot re-derive (#2462). A short session is unaffected:
	// the reserve only binds when the first section would have eaten it.
	conclusions := dedupeStatus(selectConclusions(assistants))
	if len(conclusions) > 0 {
		if reserved := budget / 2; reserved > 0 {
			appendSectionWithin("What was asked", dedupeStatus(users), budget-reserved)
		} else {
			appendSection("What was asked", dedupeStatus(users))
		}
	} else {
		appendSection("What was asked", dedupeStatus(users))
	}
	appendSection("What the assistant concluded", conclusions)
	return strings.TrimSpace(b.String()) + "\n"
}

// MessageText is the readable prose of one message: redacted, noise lines
// dropped, and capped. Text with a code fence is kept whole.
func MessageText(s string) string {
	s = strings.TrimSpace(redact.SafeForDisplay(s))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	var keep []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || noisyMessage(line) || noiseLine(line) || !looksLikeProse(line) {
			continue
		}
		keep = append(keep, line)
		if len(keep) >= 16 {
			break
		}
	}
	return strings.Join(strings.Fields(strings.Join(keep, " ")), " ")
}

var (
	shareLineNumRE = regexp.MustCompile(`^\s*\d{1,6}[ \t]`)         // "1\tdiff --git", numbered dumps
	shareGrepRE    = regexp.MustCompile(`^\S+\.[a-z]{1,5}:\d+[:)]`) // path/file.go:18: grep output
	shareShellRE   = regexp.MustCompile(`^\((eval|\w*sh)\):\d*:?`)  // zsh/bash error prefixes
	shareDigitsRE  = regexp.MustCompile(`^[\d\s.,%-]+$`)            // bare number sequences
)

func looksLikeProse(line string) bool {
	// Short lines are kept: dumps are long. The prose gate exists to drop
	// pasted JSON/CLI walls, not three-word problem statements.
	if len(line) < 80 {
		return true
	}
	letters, total, wordish, spaceless := 0, 0, 0, 0
	for f := range strings.FieldsSeq(line) {
		hasLetter := false
		for _, r := range f {
			if unicode.IsLetter(r) {
				hasLetter = true
			}
		}
		if hasLetter && len(f) >= 2 {
			wordish++
		}
	}
	for _, r := range line {
		total++
		if unicode.IsLetter(r) {
			letters++
		}
		if cjkfold.Unspaced(r) {
			spaceless++
		}
	}
	if total == 0 {
		return false
	}
	// enough real words, and letters are a real share of the characters.
	// Chinese, Japanese and Korean prose is written without spaces, so Fields
	// sees one long token and the word count reads a paragraph of it as a
	// pasted dump: every assistant answer over 80 bytes in those scripts was
	// dropped from digests, and a session written in them recalled nothing
	// (#1340). Their characters are the words — but a share of the line, not a
	// handful of them, or a JSON blob with Chinese values reads as a sentence.
	if spaceless >= 8 && spaceless*100/total >= 50 {
		return true
	}
	return wordish >= 4 && letters*100/total >= 45
}

func noiseLine(line string) bool {
	return strings.HasPrefix(line, "|") || numberedDumpLine(line) ||
		shareGrepRE.MatchString(line) || shareShellRE.MatchString(line) ||
		shareDigitsRE.MatchString(line) || looksLikeListingDump(line)
}

// numberedDumpLine reports whether a line that opens with a number is a line
// out of a numbered listing rather than a sentence that starts with one.
//
// The rule used to be the number alone, which dropped every sentence opening
// with a count — "12 зелёных. Жду ревьюера перед мержем.", "505 отказов записи
// за 16 часов". In real-world stores, of the lines it matched in assistant
// text, 917 were sentences and none was a listing, because a numbered listing
// arrives inside a code fence and MessageText returns those whole.
//
// What separates them, read off the same store rather than guessed: `cat -n`
// and the file readers put a tab after the number, and the dumps that use a
// space — `3 files changed, 9 insertions(+)`, `300 index.js` — are fragments
// with no sentence in them. So a space-separated line survives only if it
// reads as one.
func numberedDumpLine(line string) bool {
	if !shareLineNumRE.MatchString(line) {
		return false
	}
	rest := strings.TrimLeft(line, " ")
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i < len(rest) && rest[i] == '\t' {
		return true
	}
	body := strings.TrimSpace(rest[i:])
	// A pipe opens a table row, which is what the numbered rows of a rendered
	// listing look like.
	if strings.HasPrefix(body, "|") {
		return true
	}
	return !hasSentenceEnd(body)
}

// hasSentenceEnd reports whether the text closes a sentence anywhere, by the
// same rule firstSentences counts them: a stop with a space after it, or one
// of the CJK stops, which take no space.
func hasSentenceEnd(s string) bool {
	for i, r := range s {
		switch {
		case r == '.' || r == '!' || r == '?':
			next := i + utf8.RuneLen(r)
			if next >= len(s) || s[next] == ' ' {
				return true
			}
		case isCJKSentenceEnd(r):
			return true
		}
	}
	return false
}

// shareStopwords: a line of 8+ tokens with none of these is a path listing or
// ls dump, not a sentence anyone wrote.
var shareStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "to": true,
	"of": true, "in": true, "on": true, "and": true, "or": true, "it": true,
	"we": true, "i": true, "you": true, "not": true, "with": true, "for": true,
	"и": true, "в": true, "на": true, "не": true, "что": true, "как": true,
	"это": true, "у": true, "с": true, "по": true, "а": true, "но": true,
}

// IsDocumentItem reports whether a line is one item of a document the agent
// wrote rather than something it said about the work.
//
// A message can be a draft, a spec, a checklist — a text produced during the
// session rather than a statement about it. In real-world stores, the
// session-start block recalled "- Keep responses concise by default; terse
// mode active: lite." to a later agent as a past decision; it is one bullet of
// a 130-line specification the agent had just written. The prose around such a
// document still describes the work and is left alone, and so is a short list
// inside an ordinary reply — this asks that the message be mostly structure.
func IsDocumentItem(message, line string) bool {
	if !isStructureLine(line) {
		return false
	}
	lines, marked := 0, 0
	for l := range strings.SplitSeq(message, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines++
		if isStructureLine(l) {
			marked++
		}
	}
	return lines >= documentLines && marked*10 >= lines*documentShare
}

// documentLines is how long a message must be before its shape says anything.
// A four-line reply with three bullets is a normal answer.
const documentLines = 20

// documentShare is how much of it must be structure, in tenths.
const documentShare = 8

func isStructureLine(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false
	}
	if t[0] == '|' {
		return true
	}
	// The marker has to be followed by a space, or it is not a marker: a line
	// opening in bold — "**1. caffeinate in a separate terminal**" — starts
	// with an asterisk and is a sentence, and "-> then the retry fires" starts
	// with a dash. Both were counted as structure by the first version of this,
	// which is how a real conclusion could be mistaken for a list item.
	rest := strings.TrimLeft(t, "#")
	if len(rest) < len(t) {
		return rest == "" || strings.HasPrefix(rest, " ")
	}
	switch t[0] {
	case '-', '*', '+':
		return strings.HasPrefix(t[1:], " ")
	}
	return false
}

// IsListingDump reports whether a line is a listing rather than a sentence.
// Exported for the snippet path, which had its own smaller pair of filters —
// a numbered-line check and a tool-dump check — and no notion of a listing at
// all, so `wc -l` output and a run of paths were quoted as the passage that
// explains a file.
func IsListingDump(line string) bool { return looksLikeListingDump(line) }

func looksLikeListingDump(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return false
	}
	words := 0
	for _, f := range fields {
		if plainWordField(f) {
			words++
		}
		if shareStopwords[strings.ToLower(strings.Trim(f, ".,!?:;"))] {
			return false
		}
	}
	// Most of a sentence is words; most of a dump is not. The path count this
	// used to keep was never read, so the rule degenerated into "eight fields
	// and none of them a stopword" — which is an ordinary sentence in any
	// language the stopword list does not cover, and the list has eighteen
	// English words against twelve Russian. Measured over 277375 prose lines
	// of a real store, it dropped 20294 of them; 55 were listings.
	return words*2 < len(fields)
}

// plainWordField reports whether a token is an ordinary word: letters only,
// once the punctuation a sentence puts around one is off. A path, a filename,
// a mode string, a size and a timestamp are all not — which is what an `ls`
// line is made of, and what a sentence is not.
func plainWordField(f string) bool {
	f = strings.Trim(f, "`\"'()[]{},;:.!?—–-*_#")
	if utf8.RuneCountInString(f) < 2 {
		return false
	}
	for _, r := range f {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// IsPlumbing reports whether a message is a harness envelope rather than
// something a person or an agent said — an inter-agent notification, a tool
// frame, a pasted blob. The digest has always dropped these; recall quoted them
// into the session-start block, where they were among the first lines an agent
// read.
func IsPlumbing(s string) bool { return noisyMessage(s) }

// toolCallRecordRE matches a harness marker, a tool name and the opening of a
// JSON object of arguments — the shape a captured tool-call log has.
var toolCallRecordRE = regexp.MustCompile(`[⚙⏺▶►]\s*[A-Za-z_][\w.:-]*\s*\{\s*"`)

// IsToolCallRecord reports whether a line records a call being made rather than
// anything anyone said.
//
// An agent run from inside a session has its stdout captured into that
// session's transcript, so the log of the queries it sent lands in an assistant
// message — where the tool role, which is how deja separates machine output,
// never applies. A later question then matches the record of that same question
// being asked, and it outranks the answer: asked which wording was picked for
// the repository description, recall's top hit was a line reading
// `⚙ deja_recall {"query":"deja-vu repository descriptio…` and the agent
// invented the rest (#2067).
//
// Barred from matching rather than dropped at ingest. That a call happened is
// what `deja how` and `deja fix` are built on, and this only says such a line
// is not a candidate for the slot that answers a question.
func IsToolCallRecord(line string) bool {
	return toolCallRecordRE.MatchString(line)
}

func noisyMessage(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	// Tag-shaped envelopes anywhere in the line, not only at its start. A
	// harness that wraps one — "Another Claude session sent a message:
	// <teammate-message ...>" — slipped past the prefix check and reached the
	// session-start block, where truncated inter-agent JSON was among the first
	// things an agent read. Nobody writes these tags in prose.
	for _, p := range []string{"<local-command", "<command-", "<task-notification", "<teammate-message", "<bash-", "<system-reminder", "<deja-recall"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	// Prose, so only where it opens the message.
	if strings.HasPrefix(t, "Caveat:") || IsCompactionSummary(t) || IsHookStatusLine(t) || harnessNotice(t) {
		return true
	}
	if strings.Contains(t, "tool_use") || strings.Contains(t, "tool_result") {
		return true
	}
	return looksLikeDataDump(t)
}

// harnessNotice is a line the harness wrote in the assistant's place: a usage
// limit, a billing notice, or an API error. It ends a turn, so it was picked as
// what the turn concluded.
func harnessNotice(t string) bool {
	if len(t) > 300 {
		return false
	}
	if strings.HasPrefix(t, "API Error: ") || strings.HasPrefix(t, "Claude Code can't use your ") {
		return true
	}
	return (strings.HasPrefix(t, "You've hit your ") || strings.HasPrefix(t, "You've reached your ")) &&
		strings.Contains(t, "limit")
}

// looksLikeDataDump flags pasted JSON, CLI output, or blobs with very long
// unbroken tokens — content that would make a shared digest unreadable.
func looksLikeDataDump(t string) bool {
	if len(t) > 400 {
		if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && strings.Contains(t, "\":\"") {
			return true
		}
	}
	longestRun := 0
	run := 0
	for _, r := range t {
		if r == ' ' || r == '\n' || r == '\t' {
			run = 0
			continue
		}
		run++
		if run > longestRun {
			longestRun = run
		}
	}
	return longestRun > 200
}

// cutMarker is how this package says a passage was cut. It ends the block, so
// it carries the paragraph break with it.
const cutMarker = "…\n\n"

// cutMark is the marker without its trailing blank line, for a caller checking
// how a block ended.
const cutMark = "…"

// cutMarked trims a passage to n bytes and says that it was cut. A block handed
// to a person or an agent that simply stops reads as a finished thought, and the
// end of a message is where a session says it changed its mind — the same defect
// #1336 fixed for conclusions. The marker is paid for out of the budget rather
// than added to it.
func cutMarked(s string, n int) string {
	if n <= len(cutMarker) {
		return ""
	}
	limit := n - len(cutMarker)
	raw := UTF8SafeCut(s, limit)
	t := strings.TrimRight(raw, " \t\n")
	if limit < len(s) && len(t) == len(raw) {
		if i := strings.LastIndexAny(t, " \t\n"); i > 0 && len(t)-i <= 24 {
			t = strings.TrimRight(t[:i], " \t\n.,;:-")
		}
	}
	if t == "" {
		return ""
	}
	if strings.HasSuffix(t, "…") {
		return t + "\n\n"
	}
	return t + cutMarker
}

// UTF8SafeCut cuts s to at most n bytes without splitting a rune.
func UTF8SafeCut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ProjectNameCandidates is every name the sessions of the project at cwd may
// carry. Under a path the alias file names, that name comes first, then the
// names the directory had without it: sessions and notes indexed before the
// file existed still carry those until a rebuild.
func ProjectNameCandidates(cwd string) []string {
	var names []string
	add := func(name string) {
		if name == "" || slices.Contains(names, name) {
			return
		}
		names = append(names, name)
	}
	if name, ok := sources.ProjectAliasFor(cwd); ok {
		add(name)
	}
	cwd = projectRoot(cwd)
	if isHomeOrRoot(cwd) {
		return names
	}
	add(sources.PlainProjectName(cwd))
	if base := filepath.Base(cwd); base != "" {
		add(filepath.Base(filepath.Dir(cwd)) + "/" + base)
		add(base)
	}
	// The same repo appears under every worktree's path; sessions recorded in
	// one worktree belong to the project, not to that checkout. Each worktree
	// root contributes its name forms so recall sees one project. Two
	// different repos that merely share a basename stay separate everywhere
	// the full encoded path matches first.
	for _, root := range gitWorktreeRoots(cwd) {
		if name, ok := sources.ProjectAliasFor(root); ok {
			add(name)
		}
		if isHomeOrRoot(root) {
			continue
		}
		add(sources.PlainProjectName(root))
		if base := filepath.Base(root); base != "" {
			add(filepath.Base(filepath.Dir(root)) + "/" + base)
			add(base)
		}
	}
	return names
}

// isHomeOrRoot reports whether p is the filesystem root or the user's home
// directory. Neither is a project: a session started in ~ or under a dotfiles
// repo at ~ has no project in common with the next unrelated session run from
// there (#5750).
func isHomeOrRoot(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return true
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == "/" || clean == string(filepath.Separator) || filepath.Dir(clean) == clean {
		return true
	}
	home := sources.Home()
	if home == "" {
		return false
	}
	if strings.EqualFold(clean, filepath.Clean(home)) {
		return true
	}
	if r1, err1 := filepath.EvalSymlinks(clean); err1 == nil {
		if r2, err2 := filepath.EvalSymlinks(home); err2 == nil && strings.EqualFold(r1, r2) {
			return true
		}
	}
	return false
}

// PrimaryProjectName is the first of ProjectNameCandidates, the one a caller
// keys on, without listing the worktrees that only add names after it.
func PrimaryProjectName(cwd string) string {
	return sources.ClaudeProjectName(projectRoot(cwd))
}

// ProjectLabel is the name the rows give the project at cwd, and the one
// --project takes: the alias, or the parent/base form of the directory. Not
// PrimaryProjectName, which decodes the directory as a Claude project dir and
// so reads `my-app` as `my/app` (#5321).
func ProjectLabel(cwd string) string {
	if name, ok := sources.ProjectAliasFor(cwd); ok {
		return name
	}
	return sources.CWDProjectName(projectRoot(cwd))
}

// ProjectDirs returns the directory paths that identify the project at cwd:
// the workspace root, cwd itself, and any git worktree roots, excluding home
// and filesystem root.
func ProjectDirs(cwd string) []string {
	root := projectRoot(cwd)
	if isHomeOrRoot(root) {
		return nil
	}
	var dirs []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" || isHomeOrRoot(d) {
			return
		}
		d = filepath.Clean(d)
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	add(cwd)
	add(root)
	for _, wt := range gitWorktreeRoots(root) {
		add(wt)
	}
	return dirs
}

// projectRoot is where the names are taken from: a WorkBuddy chat folder is
// named for the workspace root it sits in, as the session recorded there is
// (#4735).
func projectRoot(cwd string) string {
	if root, ok := sources.ChatWorkspaceRoot(cwd); ok {
		return root
	}
	return cwd
}

// worktreeListBudget is how long the root lookup may take before it gives up.
//
// The call is cheap where processes are cheap — measured at 5 ms on a fresh
// repository here — and the budget is there so a session-start hook cannot hang
// on a slow disk. A cold `git.exe` on windows is nothing like 5 ms: the CI
// runner missed 400 ms on exactly this call, and what a user loses when it does
// is the project scoping for an agent started in a subdirectory, silently
// (#3624).
var worktreeListBudget = func() time.Duration {
	if runtime.GOOS == "windows" {
		return 2 * time.Second
	}
	return 400 * time.Millisecond
}()

// gitWorktreeRoots lists the repo's worktree roots (including the main one)
// when cwd is inside a git repository. Best effort with a hard timeout: no
// git, no repo, or a slow disk simply yields nothing.
func gitWorktreeRoots(cwd string) []string {
	top, gitInfo, ok := findGitTop(cwd)
	if !ok {
		return nil
	}
	// Fast path for the common case: a standard checkout whose .git directory
	// has no linked worktrees. Resolving the root in-process avoids spawning git
	// on every hook/query invocation and prevents cold-process timeouts (#3624).
	if gitInfo.IsDir() {
		if _, err := os.Stat(filepath.Join(top, ".git", "worktrees")); err != nil {
			if resolved, err := filepath.EvalSymlinks(top); err == nil {
				top = resolved
			}
			if strings.Contains(filepath.ToSlash(top), "/.git/") {
				return nil
			}
			return []string{top}
		}
	}
	cacheKey := worktreeCacheKey(top, gitInfo)
	if cacheKey != "" {
		if v, ok := worktreeRootsCache.Load(cacheKey); ok {
			if cached, ok := v.([]string); ok {
				return slices.Clone(cached)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeListBudget)
	defer cancel()
	out, err := gitread.Command(ctx, "-C", cwd, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	// Every root, including a lone one. It used to be dropped as "nothing
	// beyond cwd's own names", which holds only when the caller is standing at
	// that root: from a package directory inside it the root is the one name
	// the caller does not have, and it is the project recall is scoped by — so
	// an agent started anywhere but the top of its repository had no memory at
	// all (#2037). At the root itself the names are the same computation on the
	// same path and dedupe away, and a root spelled differently — through a
	// symlink — is a spelling recall wants to see.
	var roots []string
	for line := range strings.SplitSeq(string(out), "\n") {
		p, ok := strings.CutPrefix(line, "worktree ")
		p = strings.TrimSpace(p)
		if !ok || p == "" {
			continue
		}
		// Inside a submodule git names the gitdir rather than the working tree,
		// and ".git/modules/sub" is not a project anybody worked in.
		if strings.Contains(filepath.ToSlash(p), "/.git/") {
			continue
		}
		roots = append(roots, p)
	}
	if cacheKey != "" {
		worktreeRootsCache.Store(cacheKey, slices.Clone(roots))
	}
	return roots
}

var worktreeRootsCache sync.Map

func findGitTop(cwd string) (string, os.FileInfo, bool) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", nil, false
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = filepath.Clean(cwd)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, info, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil, false
		}
		dir = parent
	}
}

func worktreeCacheKey(top string, gitInfo os.FileInfo) string {
	dotGit := filepath.Join(top, ".git")
	if gitInfo.IsDir() {
		wtDir := filepath.Join(dotGit, "worktrees")
		info, err := os.Stat(wtDir)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%s|%d", top, info.ModTime().UnixNano())
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return fmt.Sprintf("%s|%d", top, gitInfo.ModTime().UnixNano())
	}
	line := strings.TrimSpace(string(b))
	if gitdir, ok := strings.CutPrefix(line, "gitdir:"); ok {
		gitdir = strings.TrimSpace(gitdir)
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(top, gitdir)
		}
		wtParent := filepath.Dir(filepath.Clean(gitdir))
		if info, err := os.Stat(wtParent); err == nil {
			return fmt.Sprintf("%s|%d|%d", top, gitInfo.ModTime().UnixNano(), info.ModTime().UnixNano())
		}
	}
	return fmt.Sprintf("%s|%d", top, gitInfo.ModTime().UnixNano())
}

// agentArtifactMarkers flag transcript entries that are tool output or
// harness plumbing recorded under a user/assistant role — noise that would
// bury the actual problem statement in a handoff.
var agentArtifactMarkers = []string{
	"<system-reminder>",
	"</teammate-message>",
	"<task-notification>",
	"<command-name>",
	"Bash completed with no output",
	"Shell cwd was reset",
	"tool_use_error",
	"no need to Read it back)",
	"Called the Read tool with",
	"[Request interrupted by user]",
	"Comments on artifact URI:",
	"idle_notification",
	`{"type":`,
}

// isReviewVerdict reports whether the text is a review agent's report rather
// than something concluded about the work.
//
// These read as decisions to every rule here — they are full of "approved",
// "fixed", "findings" — and they are about one diff on one day, which makes them
// the worst thing to carry forward. Read from the lines deja would have served
// before an edit on a real store: `README.md — last session on it ended:
// VERDICT: APPROVED ISSUES: - None.`, `index.tsx — No findings.`, and worst,
// `config.go — prior decision: Deploy: NO-DEPLOY until at least findings 1 and 2
// are fixed.` — a hold from a review of something else entirely, handed to an
// agent months later as the standing position on that file.
func isReviewVerdict(trimmed string) bool {
	if strings.Contains(trimmed, "REQUIRED_FIXES:") || strings.Contains(trimmed, "NO-DEPLOY") {
		return true
	}
	for _, p := range []string{"VERDICT:", "Findings:", "No findings", "Deploy:"} {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// IsAgentArtifact reports whether text carries one of agentArtifactMarkers.
func IsAgentArtifact(text string) bool {
	for _, m := range agentArtifactMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	// deja's own block echoed back by the harness is plumbing wherever it
	// sits in the message (#3178).
	if strings.Contains(text, "<deja-recall>") {
		return true
	}
	trimmed := strings.TrimSpace(text)
	if IsCompactionSummary(trimmed) || IsHookStatusLine(trimmed) || isReviewVerdict(trimmed) || harnessNotice(trimmed) {
		return true
	}
	// Harness preambles injected as user turns: <environment_context>,
	// <user_instructions> and similar XML-wrapped plumbing.
	if strings.HasPrefix(trimmed, "<") && strings.Contains(trimmed, "</") {
		return true
	}
	// ls dumps recorded under a user role.
	if strings.HasPrefix(trimmed, "total ") && strings.Contains(trimmed, "rwx") {
		return true
	}
	// Tool echoes: file writes, diffs, command transcripts.
	for _, p := range []string{"File created successfully at:", "The file ", "diff --git ", "$ "} {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// Long dumps with almost no prose: measure letters vs symbols/digits in
	// the first few hundred bytes — listings and tables sit far below prose.
	if len(trimmed) > 400 {
		letters, others := 0, 0
		for _, r := range trimmed[:400] {
			switch {
			case r == ' ':
			case unicode.IsLetter(r):
				letters++
			default:
				others++
			}
		}
		if others > letters {
			return true
		}
	}
	return false
}
