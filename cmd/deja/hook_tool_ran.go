package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// An agent about to edit a file is told what was decided about it, and then it
// has to work out for itself how to check the edit — which on a repository of
// any size is the expensive part: measured on a 325-file fixture, ten to
// sixteen reads went into finding the command that runs the suite for one
// package, after deja had already said what the code does.
//
// The sessions that touched the file ran that command, and sessionfacts.gob
// records which of them the transcript saw pass. So the moment the file is
// opened is the moment to hand it over — the same join the file line already
// makes, one table further.
const (
	// toolHookMinRanSessions is the bar. One session running something is a
	// keystroke; two that both touched this file and both ended cleanly on the
	// same command is the project's way of checking it.
	toolHookMinRanSessions = 2
	// ranLabel is what the line says it is handing over. Not "the way to test
	// this" — a command that passed in two sessions is evidence, and the
	// sentence claims exactly that much.
	ranLabel = " — ran here and passed: "
	// failLabel is the other half of the same fact. A check that was red in
	// the last two sessions is worth as much as one that was green: an agent
	// told nothing runs it, watches it fail, and spends the turn deciding
	// whether it broke something.
	failLabel = " — ran here and failed: "
	// toolHookRanMax is where a command stops being one and starts being a
	// paragraph in a line the agent reads on every action.
	toolHookRanMax = 90
)

// ranCandidate is one command that passed in the sessions that touched a file.
type ranCandidate struct {
	text     string
	sessions int
	last     time.Time
	// rank is how near the end of a session this command ran, best over the
	// sessions holding it: the entry keeps them most recent first, so a low
	// rank is a command a session ended on rather than one it moved past.
	rank int
	// failed says the transcript saw it end badly, and exit is what with. The
	// two are one candidate list rather than two, so a command that passes in
	// three sessions still wins over one that failed in two.
	failed bool
	exit   int
	// targeted says the command names this file's stem or package rather than
	// sweeping the whole repository (`go test ./...`, `make test`).
	targeted bool
}

// ranBetter orders two candidates: the one more sessions ran, then the one
// nearer the end of a session, then the more recent, then the text. The last
// two are there so the line is the same on every call — `make test` and the
// command that finally made it pass are usually in the same two sessions, and
// a map's order would have decided which one the agent was handed.
func ranBetter(a, b ranCandidate) bool {
	switch {
	case a.failed != b.failed:
		// What works outranks what does not, at any count: the reader is
		// about to change this file, and one of the two is a thing to run.
		return b.failed
	case !a.failed && a.targeted != b.targeted:
		// A passing check that targets this file or its package outranks a
		// repo-wide sweep once both meet the session floor.
		return a.targeted
	case a.sessions != b.sessions:
		return a.sessions > b.sessions
	case a.rank != b.rank:
		return a.rank < b.rank
	case !a.last.Equal(b.last):
		return a.last.After(b.last)
	}
	return a.text < b.text
}

func fileHookRanLineFor(dir, path string, metas []index.SessionMeta) string {
	if len(metas) < toolHookMinRanSessions {
		return ""
	}
	facts := index.ReadSessionFacts(dir)
	if len(facts) == 0 {
		return ""
	}
	by := map[string]*ranCandidate{}
	// One row per session, whatever the caller handed over: the manifest is
	// matched on a basename, so a session that touched two files of the same
	// name arrives twice, and the number in the line is sessions.
	counted := map[string]bool{}
	for _, m := range metas {
		key := m.Key()
		if counted[key] {
			continue
		}
		counted[key] = true
		f, ok := facts[key]
		if !ok {
			continue
		}
		// One session counts once for a command however often it ran it: the
		// number in the line is sessions, and a loop of forty retries is one
		// session's opinion.
		seen := map[string]bool{}
		for pos, c := range f.Commands {
			if !c.Known {
				continue
			}
			// "Ran here and passed" is true of a great deal that says nothing
			// about the file. Read off a real store, the top candidate for
			// two files in this repository was `kill %1; pkill -f 'codex
			// exec'; echo …` — cleanup, run in two sessions, exit 0, and
			// worth none of the bytes it costs at every action. So the line
			// is restricted to the commands that check the work.
			cmd := orientCommand(c.Text)
			if cmd == "" || !index.VerifyCommand(cmd) {
				continue
			}
			ck := normalizedCommandText(cmd)
			if ck == "" || seen[ck] {
				continue
			}
			seen[ck] = true
			if !c.Passed() {
				ck = "!" + ck
			}
			a := by[ck]
			if a == nil {
				a = &ranCandidate{
					text:     cmd,
					rank:     pos,
					failed:   !c.Passed(),
					exit:     c.Exit,
					targeted: commandTargetsFile(cmd, path),
				}
				by[ck] = a
			}
			a.sessions++
			if pos < a.rank {
				a.rank = pos
			}
			if m.Updated.After(a.last) {
				a.last = m.Updated
			}
		}
	}
	var best *ranCandidate
	for _, a := range by {
		if a.sessions < toolHookMinRanSessions {
			continue
		}
		if best == nil || ranBetter(*a, *best) {
			best = a
		}
	}
	if best == nil {
		return ""
	}
	// SafeCommand, not the quoting a pasted value gets: this is handed over to
	// be run, and wrapping it in quotes makes the whole line one word. What it
	// does strip is what a terminal would obey, and the newline that would end
	// deja's own sentence and start one that reads as deja speaking to the
	// agent (#1863). The spacing inside it is kept, because `-run "Pool  Size"`
	// is a different test filter from `-run "Pool Size"` (#2052).
	cmd := truncateToolLine(search.SafeCommand(best.text), toolHookRanMax)
	if best.failed {
		return fmt.Sprintf("%s`%s` — exit %d in %s", failLabel, cmd, best.exit, toolSessionCount(best.sessions)) //nolint:gocritic // %#q falls back to double quotes for a command with a backquote
	}
	return fmt.Sprintf("%s`%s` (%s)", ranLabel, cmd, toolSessionCount(best.sessions)) //nolint:gocritic // %#q falls back to double quotes for a command with a backquote
}

// commandTargetsFile reports whether cmd specifically names the file's stem,
// package directory, or a test filter (`-run`, `-k`, `--grep`) rather than a
// repo-wide sweep (`go test ./...`, `make test`).
func commandTargetsFile(cmd, path string) bool {
	if path == "" || cmd == "" {
		return false
	}
	lowCmd := strings.ToLower(cmd)
	base := strings.ToLower(filepath.Base(path))
	stem := base
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}
	stem = strings.TrimSuffix(stem, "_test")
	dirBase := strings.ToLower(filepath.Base(filepath.Dir(path)))
	if len(stem) >= 3 && strings.Contains(lowCmd, stem) {
		return true
	}
	if len(dirBase) >= 3 && dirBase != "." && dirBase != "/" &&
		dirBase != "src" && dirBase != "pkg" && dirBase != "cmd" && dirBase != "internal" {
		if strings.Contains(lowCmd, "/"+dirBase) || strings.HasSuffix(lowCmd, " "+dirBase) || strings.Contains(lowCmd, " "+dirBase+" ") {
			return true
		}
	}
	return strings.Contains(lowCmd, " -run ") || strings.Contains(lowCmd, " -k ") || strings.Contains(lowCmd, " --grep ")
}

const (
	// toolHookMinCoChangeSessions is the minimum focused session count before a
	// companion file is surfaced at the point of an edit.
	toolHookMinCoChangeSessions = 3
	// toolHookMaxFocusedFiles excludes wide repository sweeps so a rename across
	// twenty files does not pair every file with every other.
	toolHookMaxFocusedFiles = 8
)

// coChangeCompanionIn finds the companion or test file that sessions touching
// path almost always modified alongside it (at least 3 sessions and 2/3 of the
// focused sessions that touched path).
func coChangeCompanionIn(cwd, path string, metas []index.SessionMeta) (string, int, int) {
	targetBase := strings.ToLower(baseName(path))
	if targetBase == "" {
		return "", 0, 0
	}
	counted := map[string]bool{}
	companionCounts := map[string]int{}
	companionDisplay := map[string]string{}
	focused := 0
	tree := orientRepoTree(cwd)
	checkDisk := tree.exists(orientPath(path, cwd)) || tree.exists(targetBase)
	for _, m := range metas {
		key := m.Key()
		if counted[key] {
			continue
		}
		counted[key] = true
		if len(m.Touched) == 0 {
			continue
		}
		seenInSession := map[string]bool{}
		var companions []string
		hasTarget := false
		for _, t := range m.Touched {
			if isScratch(t) {
				continue
			}
			norm := orientPath(t, cwd)
			if norm == "" {
				norm = baseName(t)
			}
			if isOrientEphemeralFile(norm) {
				continue
			}
			if checkDisk && !tree.exists(norm) {
				continue
			}
			b := baseName(norm)
			low := strings.ToLower(b)
			if low == "" {
				continue
			}
			if low == targetBase {
				hasTarget = true
				continue
			}
			display := b
			if isAmbiguousCoChangeBase(low) && !filepath.IsAbs(norm) && strings.Contains(filepath.ToSlash(norm), "/") {
				display = filepath.ToSlash(norm)
				low = strings.ToLower(display)
			}
			if seenInSession[low] {
				continue
			}
			seenInSession[low] = true
			companions = append(companions, low)
			if companionDisplay[low] == "" {
				companionDisplay[low] = display
			}
		}
		if !hasTarget || len(companions) == 0 || len(companions)+1 > toolHookMaxFocusedFiles {
			continue
		}
		focused++
		for _, low := range companions {
			companionCounts[low]++
		}
	}
	if focused < toolHookMinCoChangeSessions {
		return "", 0, 0
	}
	bestLow := ""
	bestCount := 0
	for low, c := range companionCounts {
		if c < toolHookMinCoChangeSessions || c*3 < focused*2 {
			continue
		}
		if c > bestCount || (c == bestCount && coChangeCompanionBetter(low, bestLow)) {
			bestLow = low
			bestCount = c
		}
	}
	if bestLow == "" {
		return "", 0, 0
	}
	return search.SafePath(companionDisplay[bestLow]), bestCount, focused
}

// fileHookCoChangeLineIn names the companion or test file that sessions touching
// path almost always modified alongside it (at least 3 sessions and 2/3 of the
// focused sessions that touched path).
func fileHookCoChangeLineIn(cwd, path string, metas []index.SessionMeta) string {
	companion, bestCount, focused := coChangeCompanionIn(cwd, path, metas)
	if companion == "" {
		return ""
	}
	return fmt.Sprintf(" — usually edited alongside %s (%d of %d sessions)",
		companion, bestCount, focused)
}

func coChangeCompanionBetter(a, b string) bool {
	aTest := strings.Contains(a, "test") || strings.Contains(a, "spec")
	bTest := strings.Contains(b, "test") || strings.Contains(b, "spec")
	if aTest != bTest {
		return aTest
	}
	return a < b
}

func isAmbiguousCoChangeBase(low string) bool {
	switch low {
	case "index.js", "index.ts", "index.tsx", "index.jsx", "main.go", "mod.rs", "lib.rs",
		"init.py", "__init__.py", "route.ts", "page.tsx", "layout.tsx", "config.go",
		"types.go", "utils.go", "helpers.go", "client.go", "server.go":
		return true
	}
	return false
}

// siblingTestLineFor resolves the focused test filter for an edited symbol
// by scanning sibling test files in the same directory across Go, Python,
// TS/JS, and Rust when session history has no prior verification command.
func siblingTestLineFor(cwd, path string, editSymbols []string) string {
	if path == "" || len(editSymbols) == 0 {
		return ""
	}
	diskPath := path
	if !filepath.IsAbs(diskPath) && cwd != "" {
		diskPath = filepath.Join(cwd, diskPath)
	}
	pkgDir := filepath.Dir(diskPath)
	ext := strings.ToLower(filepath.Ext(diskPath))
	switch ext {
	case ".go":
		return goSiblingTestCmd(cwd, diskPath, pkgDir, editSymbols)
	case ".py":
		return pySiblingTestTarget(cwd, diskPath, pkgDir, editSymbols)
	case ".ts", ".tsx", ".js", ".jsx":
		return tsSiblingTestTarget(cwd, diskPath, pkgDir, ext, editSymbols)
	case ".rs":
		return rustSiblingTestTarget(cwd, diskPath, pkgDir, editSymbols)
	}
	return ""
}

func goSiblingTestCmd(cwd, diskPath, pkgDir string, editSymbols []string) string {
	if strings.HasSuffix(diskPath, "_test.go") {
		return ""
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(diskPath), ".go")
	preferred := stem + "_test.go"
	var testFiles []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if e.Name() == preferred {
			testFiles = append([]string{e.Name()}, testFiles...)
		} else {
			testFiles = append(testFiles, e.Name())
		}
	}
	if len(testFiles) > 2500 {
		testFiles = testFiles[:2500]
	}
	var matched []string
	seen := map[string]bool{}
	for _, tf := range testFiles {
		raw, err := os.ReadFile(filepath.Join(pkgDir, tf))
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		content := string(raw)
		if !mentionsAnySymbol(editSymbols, content) {
			continue
		}
		curTest := ""
		for line := range strings.SplitSeq(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "func Test") {
				rest := strings.TrimPrefix(trimmed, "func ")
				if id := leadingIdent(rest); strings.HasPrefix(id, "Test") && len(id) > 4 {
					curTest = id
				}
				continue
			}
			if curTest != "" && !seen[curTest] && mentionsAnySymbol(editSymbols, trimmed) {
				seen[curTest] = true
				matched = append(matched, curTest)
				if len(matched) >= 3 {
					break
				}
			}
		}
		if len(matched) >= 3 {
			break
		}
	}
	if len(matched) == 0 {
		return ""
	}
	root := sources.RepoRoot(pkgDir)
	if root == "" && cwd != "" {
		root = sources.RepoRoot(cwd)
	}
	if root == "" && cwd != "" {
		root = cwd
	}
	pkgTarget := "."
	if root != "" {
		if rel, err := filepath.Rel(root, pkgDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			pkgTarget = "./" + filepath.ToSlash(rel)
		}
	}
	cmd := fmt.Sprintf("go test %s -run '^%s$'", pkgTarget, strings.Join(matched, "|"))
	return " — test target: `" + search.SafeCommand(cmd) + "`"
}

func pySiblingTestTarget(cwd, diskPath, pkgDir string, editSymbols []string) string {
	base := filepath.Base(diskPath)
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return ""
	}
	root := repoOrWorkDir(cwd, pkgDir)
	stem := strings.TrimSuffix(base, ".py")
	candidates := []string{"test_" + stem + ".py", stem + "_test.py"}
	for _, cand := range candidates {
		candPath := filepath.Join(pkgDir, cand)
		raw, err := os.ReadFile(candPath)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		rel := cand
		if r, err := filepath.Rel(root, candPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		return pySiblingTestCmd(rel, string(raw), editSymbols)
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "test_") && !strings.HasSuffix(name, "_test.py") {
			continue
		}
		candPath := filepath.Join(pkgDir, name)
		raw, err := os.ReadFile(candPath)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		content := string(raw)
		if !mentionsAnySymbolLen(editSymbols, content, 3) {
			continue
		}
		rel := name
		if r, err := filepath.Rel(root, candPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		return pySiblingTestCmd(rel, content, editSymbols)
	}
	return ""
}

func pySiblingTestCmd(rel, content string, editSymbols []string) string {
	var matched []string
	seen := map[string]bool{}
	curTest := ""
	curMatched := false
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "def test_") {
			rest := strings.TrimPrefix(trimmed, "def ")
			idx := strings.IndexAny(rest, "(: \t")
			name := rest
			if idx > 0 {
				name = rest[:idx]
			}
			curTest = name
			curMatched = false
			if symbolInLine(curTest, editSymbols) || symbolInLine(trimmed, editSymbols) {
				if !seen[curTest] {
					seen[curTest] = true
					matched = append(matched, curTest)
				}
				curMatched = true
			}
			continue
		}
		if (strings.HasPrefix(trimmed, "def ") && !strings.HasPrefix(trimmed, "def test_")) || strings.HasPrefix(trimmed, "class ") {
			curTest = ""
			curMatched = false
			continue
		}
		if curTest != "" && !curMatched && symbolInLine(trimmed, editSymbols) {
			curMatched = true
			if !seen[curTest] {
				seen[curTest] = true
				matched = append(matched, curTest)
			}
		}
	}
	if len(matched) >= 1 && len(matched) <= 4 {
		return fmt.Sprintf(" — test target: `pytest %s -k '%s'`", search.SafePath(rel), strings.Join(matched, " or "))
	}
	return fmt.Sprintf(" — test target: `pytest %s`", search.SafePath(rel))
}

func tsSiblingTestTarget(cwd, diskPath, pkgDir, ext string, editSymbols []string) string {
	base := filepath.Base(diskPath)
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return ""
	}
	root := repoOrWorkDir(cwd, pkgDir)
	stem := strings.TrimSuffix(base, ext)
	candidates := []string{
		stem + ".test" + ext,
		stem + ".spec" + ext,
		stem + ".test.ts",
		stem + ".test.tsx",
		stem + ".test.js",
		stem + ".test.jsx",
	}
	for _, cand := range candidates {
		candPath := filepath.Join(pkgDir, cand)
		raw, err := os.ReadFile(candPath)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		rel := cand
		if r, err := filepath.Rel(root, candPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		return tsSiblingTestCmd(rel, string(raw), editSymbols)
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.Contains(name, ".test.") && !strings.Contains(name, ".spec.") {
			continue
		}
		candPath := filepath.Join(pkgDir, name)
		raw, err := os.ReadFile(candPath)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		content := string(raw)
		if !mentionsAnySymbolLen(editSymbols, content, 3) {
			continue
		}
		rel := name
		if r, err := filepath.Rel(root, candPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		return tsSiblingTestCmd(rel, content, editSymbols)
	}
	return ""
}

func tsSiblingTestCmd(rel, content string, editSymbols []string) string {
	matchedName := ""
	curTest := ""
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range []string{"it(", "test(", "it.only(", "it.skip(", "test.only(", "test.skip("} {
			if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
				if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'' || rest[0] == '`') {
					quote := rest[0]
					rest = rest[1:]
					if end := strings.IndexByte(rest, quote); end >= 0 {
						curTest = rest[:end]
					}
				}
				break
			}
		}
		if curTest != "" && symbolInLine(trimmed, editSymbols) {
			matchedName = curTest
			break
		}
	}
	if matchedName != "" {
		return fmt.Sprintf(" — test target: `npx vitest run %s -t '%s'`", search.SafePath(rel), search.SafeLine(matchedName))
	}
	return fmt.Sprintf(" — test target: `npx vitest run %s`", search.SafePath(rel))
}

func rustSiblingTestTarget(_, diskPath, pkgDir string, editSymbols []string) string {
	base := filepath.Base(diskPath)
	if strings.HasSuffix(base, "_test.rs") {
		return ""
	}
	stem := strings.TrimSuffix(base, ".rs")
	stemTest := filepath.Join(pkgDir, stem+"_test.rs")
	paths := []string{diskPath}
	if _, err := os.Stat(stemTest); err == nil {
		paths = append(paths, stemTest)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil || len(raw) > 512<<10 {
			continue
		}
		content := string(raw)
		inTest := false
		curFn := ""
		for line := range strings.SplitSeq(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#[test]") {
				inTest = true
				curFn = ""
				continue
			}
			if inTest && curFn == "" {
				for _, fnPrefix := range []string{"fn ", "pub fn ", "pub(crate) fn ", "async fn "} {
					if rest, ok := strings.CutPrefix(trimmed, fnPrefix); ok {
						curFn = leadingIdent(rest)
						break
					}
				}
				if curFn != "" {
					continue
				}
			}
			if inTest && curFn != "" {
				if symbolInLine(trimmed, editSymbols) {
					return fmt.Sprintf(" — test target: `cargo test %s`", search.SafeLine(curFn))
				}
				if strings.HasPrefix(trimmed, "#[test]") || (strings.HasPrefix(trimmed, "fn ") && curFn != "") {
					inTest = strings.HasPrefix(trimmed, "#[test]")
					curFn = ""
				}
			}
		}
	}
	return ""
}

func repoOrWorkDir(cwd, pkgDir string) string {
	root := sources.RepoRoot(pkgDir)
	if root == "" && cwd != "" {
		root = sources.RepoRoot(cwd)
	}
	if root == "" {
		return pkgDir
	}
	return root
}

func mentionsAnySymbolLen(symbols []string, text string, minLen int) bool {
	if len(symbols) == 0 || text == "" {
		return false
	}
	low := strings.ToLower(text)
	for _, sym := range symbols {
		w := strings.ToLower(strings.TrimSpace(sym))
		if len(w) < minLen {
			continue
		}
		if containsIdentifierWord(low, w) {
			return true
		}
	}
	return false
}

func symbolInLine(line string, symbols []string) bool {
	return mentionsAnySymbolLen(symbols, line, 3)
}
