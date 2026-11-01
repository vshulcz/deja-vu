package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// blameMode is what the caller asked for beyond the hits: the line answer on
// its own (`--attribution`), as JSON, and whether to record it in
// refs/notes/deja (#3723).
type blameMode struct {
	JSON        bool
	Attribution bool
	GitNote     bool
	// AllProjects answers from the whole machine. A path names a file in this
	// checkout, so by default sessions in other projects are left out.
	AllProjects bool
}

func parseBlame(args []string) (string, search.BlameOptions, blameMode, error) {
	o := search.BlameOptions{}
	mode := blameMode{}
	var path string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--":
			// The rest is the path, dash or not — the spelling search, fix and
			// how already take. A file really named `-report.md` was otherwise
			// unreachable, and the refusal reads to a caller like a file
			// nobody has ever touched.
			if i+1 < len(args) {
				if path != "" {
					return "", o, mode, errors.New("blame accepts one path")
				}
				path = args[i+1]
			}
			i++
		case "--json":
			mode.JSON = true
		case "--attribution":
			// The line answer on its own, without the file's listing under it:
			// a caller asking about a line wants one answer, and --json turns
			// it into the object that answer needs (#3723).
			mode.Attribution = true
		case "--git-note":
			mode.GitNote = true
		case "--all":
			o.All = true
		case "--all-projects":
			mode.AllProjects = true
		case "--harness", "--project", "--since":
			if i+1 >= len(args) {
				return "", o, mode, fmt.Errorf("%s needs value", a)
			}
			i++
			if strings.TrimSpace(args[i]) == "" { //nolint:gosec // bounds checked before i++
				// Empty is how "no filter" is spelled inside deja (#1612).
				return "", o, mode, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = args[i] //nolint:gosec // bounds checked before i++
			case "--project":
				o.Project = args[i] //nolint:gosec // bounds checked before i++
			case "--since":
				d, err := parseDur(args[i]) //nolint:gosec // bounds checked before i++
				if err != nil {
					return "", o, mode, err
				}
				o.Since = d
			}
		default:
			if strings.HasPrefix(a, "-") {
				if flagName(a) == "--limit" {
					// blame widens with --all rather than a count (#3405).
					return "", o, mode, fmt.Errorf("blame: unknown flag %q — it takes --all to show every session", a)
				}
				return "", o, mode, fmt.Errorf("blame: unknown flag %q; a path or question that starts with a dash goes after `--`", a)
			}
			if path != "" {
				return "", o, mode, errors.New("blame accepts one path")
			}
			path = a
		}
	}
	if path == "" {
		return "", o, mode, errors.New("blame needs a path — `deja blame <file>` or `deja blame <file>:<line>`")
	}
	return path, o, mode, nil
}

func runBlame(dir string, args []string) error {
	path, o, mode, err := parseBlame(args)
	if err != nil {
		return err
	}
	jsonOutput := mode.JSON
	// A typo'd harness must name the mistake, not read as "nobody touched this
	// file under harness X" — the same reason search and the MCP blame validate
	// it (#1113). blame has no --role to check.
	if herr := checkHarness(&o.Harness); herr != nil {
		return herr
	}
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return err
	}
	// Both of the line-level flags answer about a line and have nothing to say
	// about a file. Refusing here, by name, beats answering about the whole
	// file and leaving the caller to notice their `:42` went nowhere (#3726).
	if (mode.Attribution || mode.GitNote) && target.Line <= 0 {
		flag := "--attribution"
		if mode.GitNote {
			flag = "--git-note"
		}
		if target.LineNote != "" {
			return fmt.Errorf("blame %s: %s", flag, target.LineNote)
		}
		return fmt.Errorf("blame %s answers about one line — `deja blame %s:42 %s`", flag, path, flag)
	}
	// Before the answer, because the answer is about the file and the reader
	// asked about a line: said afterwards it reads as a footnote to a result
	// they think is line-level (#3738).
	if target.LineNote != "" && !jsonOutput {
		fmt.Fprintf(os.Stderr, "deja: %s — answering for the whole file\n", target.LineNote)
	}
	// A path names a file in this checkout. Other repos have their own
	// internal/pool/pool.go, and their sessions are about that file, not this
	// one; they were listed in among this project's.
	scope := blameScope(howCwd(), target, o.Project, mode.AllProjects)
	wantAll := o.All
	if len(scope) > 0 {
		o.All = true
	}
	hits, hidden, total, err := findBlameHits(dir, target, o, policy.ActivationSearch, os.Stderr)
	if err != nil {
		return fmt.Errorf("blame search: %w", err)
	}
	elsewhere, onlyElsewhere := 0, false
	if len(scope) > 0 {
		var in []search.BlameHit
		for _, h := range hits {
			if howProjectMatches(h.Session.Project, scope) {
				in = append(in, h)
			}
		}
		elsewhere = len(hits) - len(in)
		if len(in) > 0 {
			hits = in
		} else {
			onlyElsewhere = len(hits) > 0
		}
		total = len(hits)
		o.All = wantAll
		hits = search.CapBlame(hits, o)
	}
	if onlyElsewhere && !jsonOutput && !mode.Attribution && !mode.GitNote {
		fmt.Fprintf(os.Stderr, "deja: no session in %s mentions %s — these are from other projects\n", howScopeName(scope), target.Base)
	}
	// The line answer alone, in either rendering, and nothing about the file
	// under it: that is what was asked for.
	if mode.Attribution || mode.GitNote {
		return blameLineOnly(os.Stdout, dir, target, hits, mode)
	}
	// A line was asked about, so answer about the line first: git says which
	// commit wrote it, and the store says which session wrote the text that
	// commit replaced (#1181). Before the branches below, because a file no
	// session mentions still has a commit behind the line — and "no session
	// wrote this" is the answer, not silence.
	if target.Line > 0 && !jsonOutput {
		lineBlame(os.Stdout, dir, target, hits)
	}
	if jsonOutput {
		// The session object search returns, source and all: a consumer
		// telling local sessions from imported ones reads it on both.
		for i := range hits {
			hits[i].Session.SetSource(os.Getenv("DEJA_SOURCE_INSTANCE"))
		}
		search.PrintBlame(os.Stdout, hits, true)
		return nil
	}
	if len(hits) == 0 {
		// The file was edited — a rule withheld the session that touched it.
		// "no sessions mention it" reads as looked-and-absent, the misread
		// search and last already avoid by naming the rule (#686, #680).
		if note := policyHiddenNote(policy.ActivationSearch, hidden); note != "" {
			fmt.Fprint(os.Stderr, note)
			return nil
		}
		// And the other rule that empties this answer, which said nothing here
		// while the listing and search both named it (#2632). Before the count
		// below, for the same reason it goes before "try fewer words": a
		// denominator that includes the ignored sessions reads as a store that
		// was searched and came back empty.
		if note := ignoredHiddenNoteFor("answer", ignoredTouching(dir, target)); note != "" {
			fmt.Fprint(os.Stderr, note)
			return nil
		}
		// "run deja index" is advice for an empty store. With sessions in the
		// index it is advice for a state the tool is not in — indexing changes
		// nothing and doctor reports the stores as found — and it sends the
		// reader to fix an index that is fine (#637, same shape).
		if n, err := index.SessionCount(dir); err == nil && n > 0 {
			fmt.Fprintf(os.Stderr, "deja: no sessions mention %s — searched %d indexed session%s\n", target.Base, n, pluralS(n))
		} else {
			fmt.Fprintln(os.Stderr, emptyIndexHint("no sessions mention "+target.Base))
		}
		return nil
	}
	search.PrintBlameWidth(os.Stdout, hits, printableWidth(os.Stdout))
	// The name the reader has is the one in their editor, and a rename drops
	// everything said under the old one out of this answer (#1627).
	if note := earlierNameNote(dir, path, target); note != "" {
		fmt.Fprint(os.Stderr, note)
	}
	// blame answers "who touched this file". Ten blocks and nothing else reads
	// as all of them, the misread search already avoids with the same sentence
	// (#2299). --json keeps its bare array: a machine consumer reading a total
	// would need a different shape, and that is a break, not a note.
	if total > len(hits) {
		fmt.Fprintf(os.Stderr, "deja: showing %d of %d — add --all to see the rest\n", len(hits), total)
	}
	if elsewhere > 0 && !onlyElsewhere {
		fmt.Fprintln(os.Stderr, fitLine(os.Stderr, fmt.Sprintf("deja: %d session%s in other projects mention their own %s — --all-projects lists them", elsewhere, pluralS(elsewhere), target.Base)))
	}
	return nil
}

// blameScope is the project a blame answers from: the working directory's,
// when the file lies inside it and nothing else was asked for. A file outside
// the working directory, --project and --all-projects all leave it to the
// rest of the options.
func blameScope(cwd string, target search.BlameTarget, project string, allProjects bool) []string {
	if allProjects || strings.TrimSpace(project) != "" || cwd == "" {
		return nil
	}
	rel, err := filepath.Rel(cwd, target.FullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return howScope(cwd, "", false)
}

func findBlameHits(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer) ([]search.BlameHit, int, int, error) { //nolint:unparam // blame tests in blame_*_test.go pass it
	hits, hidden, total, _, err := blameHits(dir, target, o, activation, progress, false)
	return hits, hidden, total, err
}

// findBlameHitsStale is findBlameHits for a caller that must not wait: it
// serves the snapshot on disk while a rebuild runs, the way recall does, and
// hands the rebuild to the detached warmup. blame is the tool an agent calls
// before editing a file, so declining for the length of a refresh means the
// edit happens without the history (#1784). The CLI keeps the blocking path —
// someone typed it and is watching the progress (#1306).
func findBlameHitsStale(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer) ([]search.BlameHit, int, int, bool, error) {
	return blameHits(dir, target, o, activation, progress, true)
}

func blameHits(dir string, target search.BlameTarget, o search.BlameOptions, activation string, progress io.Writer, stale bool) ([]search.BlameHit, int, int, bool, error) {
	query := search.Options{Query: target.Stem, Harness: o.Harness, Project: o.Project, Since: o.Since, All: true}
	refreshing := false
	if stale {
		var err error
		if refreshing, err = index.EnsureForSearchStale(dir, query, progress); err != nil {
			return nil, 0, 0, false, err
		} else if refreshing && !warmupRunning(dir) {
			requestWarmup(dir)
		}
	} else if err := index.EnsureForSearch(dir, query, false, progress); err != nil {
		return nil, 0, 0, false, err
	}
	result, err := index.SearchWithRecoveryDetailed(dir, query, progress)
	if err != nil {
		return nil, 0, 0, false, err
	}
	all := search.Blame(withFileTouchers(dir, result.Sessions, target), target, o)
	visible := policyFilterBlame(activation, all)
	// Cap after the trust filter, not before: cutting first meant a withheld
	// session ate one of the ten slots, so a reader could get eight hits with
	// forty sessions still waiting behind --all.
	hits := search.CapBlame(visible, o)
	attachBlameLifecycles(hits, target)
	return hits, len(all) - len(visible), len(visible), refreshing, nil
}

// blameToucherCap bounds how many extra sessions a blame reads from the
// manifest. Measured on 500 sessions all touching one file: 0.08s uncapped
// against 0.02s capped, with the same ten at the top.
const blameToucherCap = 50

// ignoredTouching counts the sessions that touched this file and are covered by
// the ignore rule. Not IgnoredWithAllTerms, which the other surfaces use: that
// reads the text postings, and a file path reaches the index as a `files`
// record, which is served by role and never ranked — so the count for a blame
// has to come from the same Touched lists blame itself reads (#2632).
func ignoredTouching(dir string, target search.BlameTarget) int {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return 0
	}
	pol := policy.Load()
	base := strings.ToLower(filepath.Base(target.FullPath))
	n := 0
	for _, meta := range metas {
		if !pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		for _, p := range meta.Touched {
			if strings.ToLower(filepath.Base(filepath.FromSlash(p))) == base {
				n++
				break
			}
		}
	}
	return n
}

// withFileTouchers adds the sessions that edited or opened the file but never
// said its name.
//
// blame picks its candidates with an ordinary search for the file's stem, and
// the paths a tool call recorded do not answer one: the newer session that
// actually changed pool.go was invisible while an older one that only
// mentioned it in passing was the answer (#688). The manifest already knows
// which files each session touched, so this costs one metadata read.
func withFileTouchers(dir string, ss []model.Session, target search.BlameTarget) []model.Session {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ss
	}
	// Where each session sits, not merely whether it is here: search returns
	// sessions with only their matching messages attached, so one that matched
	// on speech arrives with its `files` record stripped. Skipping it as
	// "already present" is how mentioning the subject removed a session from
	// its own file's blame (#723) — it has to be replaced by the full record.
	at := make(map[string]int, len(ss))
	for i, s := range ss {
		at[s.Harness+":"+s.ID] = i
	}
	base := strings.ToLower(filepath.Base(target.FullPath))
	// Newest first, so the cap below keeps the sessions a "who last worked on
	// this" question is actually about.
	// Identity breaks ties, or which sessions survive the cap depends on Go's
	// map order and two runs disagree — the same failure as #668.
	sort.Slice(metas, func(i, j int) bool { return newestFirst(metas[i], metas[j]) })
	// Pick the identities first and read them in one pass: per-identity reads
	// stream the whole record log each time, so the cap alone did not bound
	// the cost — 50 sessions meant 50 scans (#1069).
	var want []index.Identity
	for _, meta := range metas {
		// A file like main.go is touched by everything, and each addition is a
		// record read. Ten hits are printed; this is far more than enough to
		// rank them.
		if len(want) >= blameToucherCap {
			break
		}
		for _, p := range meta.Touched {
			if strings.ToLower(filepath.Base(filepath.FromSlash(p))) != base {
				continue
			}
			want = append(want, index.Identity{Harness: meta.Harness, ID: meta.ID})
			break
		}
	}
	full, err := index.FindManyByIdentity(dir, want)
	if err != nil {
		return ss
	}
	for _, s := range full {
		// The manifest is keyed by identity, so each session reaches this once
		// — there is no second visit to record a position for.
		if i, present := at[s.Harness+":"+s.ID]; present {
			ss[i] = s
		} else {
			ss = append(ss, s)
		}
	}
	return ss
}
