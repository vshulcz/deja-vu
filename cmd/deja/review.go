package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja review [rev|range]` surfaces settled notes and prior sessions across
// the files changed in a working-tree diff, commit, or commit range.
type reviewOptions struct {
	Rev     string
	Harness string
	Project string
	Limit   int
	JSON    bool
}

const (
	reviewDefaultLimit    = 10
	reviewMaxLimit        = 100
	reviewSessionsPerFile = 3
)

type reviewEnvelopeJSON struct {
	SchemaVersion int              `json:"schema_version"`
	Target        string           `json:"target"`
	ChangedFiles  []string         `json:"changed_files"`
	Notes         []reviewNoteJSON `json:"notes"`
	Files         []reviewFileJSON `json:"files"`
}

type reviewNoteJSON struct {
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Text    string `json:"text"`
}

type reviewFileJSON struct {
	Path     string              `json:"path"`
	Sessions []reviewSessionJSON `json:"sessions"`
}

type reviewSessionJSON struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Date    string `json:"date"`
	State   string `json:"state,omitempty"`
	Note    string `json:"note,omitempty"`
	Summary string `json:"summary,omitempty"`
}

func parseReview(args []string) (reviewOptions, error) {
	o := reviewOptions{Limit: reviewDefaultLimit}
	afterDashDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			if strings.TrimSpace(a) == "" {
				return o, errors.New("review: revision cannot be empty")
			}
			if o.Rev != "" {
				return o, errors.New("review accepts one revision or range")
			}
			o.Rev = a
			continue
		}
		if a == "--" {
			afterDashDash = true
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok && strings.HasPrefix(name, "-") {
			args = append(args[:i+1], append([]string{val}, args[i+1:]...)...)
			a = name
		}
		switch a {
		case "--json":
			o.JSON = true
		case "--harness", "--project", "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs value", a)
			}
			i++
			val := strings.TrimSpace(args[i])
			if val == "" {
				return o, fmt.Errorf("%s needs value", a)
			}
			switch a {
			case "--harness":
				o.Harness = val
			case "--project":
				o.Project = val
			case "--limit":
				n, perr := strconv.Atoi(val)
				if perr != nil || n < 1 || n > reviewMaxLimit {
					return o, fmt.Errorf("--limit must be 1..%d", reviewMaxLimit)
				}
				o.Limit = n
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("review: unknown flag %q; a revision that starts with a dash goes after `--`", a)
			}
			if strings.TrimSpace(a) == "" {
				return o, errors.New("review: revision cannot be empty")
			}
			if o.Rev != "" {
				return o, errors.New("review accepts one revision or range")
			}
			o.Rev = a
		}
	}
	return o, nil
}

func runReview(dir string, args []string, w io.Writer) error {
	o, err := parseReview(args)
	if err != nil {
		return err
	}
	if herr := checkHarness(&o.Harness); herr != nil {
		return herr
	}
	cwd := howCwd()
	if cwd == "" {
		return errors.New("review needs a working directory")
	}
	rootOut, err := gitRun(cwd, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(rootOut) == "" {
		return errors.New("review needs a git repository")
	}
	repoRoot := strings.TrimSpace(rootOut)

	targetLabel, changed, err := gitReviewFiles(repoRoot, o.Rev)
	if err != nil {
		return err
	}
	if ierr := index.Ensure(dir, o.Harness, false, os.Stderr); ierr != nil {
		return ensureError(dir, ierr)
	}

	scope := howScope(repoRoot, o.Project, false)
	notes := collectReviewNotes(repoRoot, scope, changed)

	files := changed
	if o.Limit > 0 && len(files) > o.Limit {
		files = files[:o.Limit]
	}

	fileEntries := make([]reviewFileJSON, 0, len(files))
	for _, rel := range files {
		full := filepath.Join(repoRoot, filepath.FromSlash(rel))
		bt, berr := search.ResolveBlamePath(full)
		if berr != nil {
			continue
		}
		bo := search.BlameOptions{
			Harness: o.Harness,
			Project: o.Project,
			All:     len(scope) > 0,
		}
		hits, _, _, herr := findBlameHits(dir, bt, bo, policy.ActivationSearch, io.Discard)
		if herr != nil {
			continue
		}
		if len(scope) > 0 {
			var filtered []search.BlameHit
			for _, h := range hits {
				if howProjectMatches(h.Session.Project, scope) {
					filtered = append(filtered, h)
				}
			}
			hits = filtered
		}
		if len(hits) > reviewSessionsPerFile {
			hits = hits[:reviewSessionsPerFile]
		}
		sessions := make([]reviewSessionJSON, 0, len(hits))
		for _, h := range hits {
			summary := h.Title
			if len(h.Snippets) > 0 && h.Snippets[0] != "" {
				summary = h.Snippets[0]
			}
			if summary == "" {
				summary = h.Session.Title
			}
			sessions = append(sessions, reviewSessionJSON{
				Harness: h.Session.Harness,
				ID:      shortID(h.Session.ID),
				Project: search.SafeLine(h.Session.Project),
				Date:    h.Session.Updated.Local().Format("2006-01-02"),
				State:   h.Lifecycle,
				Note:    search.SafeLine(h.LifecycleNote),
				Summary: search.SafeLine(squeezeTrim(summary, 140)),
			})
		}
		fileEntries = append(fileEntries, reviewFileJSON{
			Path:     rel,
			Sessions: sessions,
		})
	}

	if o.JSON {
		if changed == nil {
			changed = []string{}
		}
		if notes == nil {
			notes = []reviewNoteJSON{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(reviewEnvelopeJSON{
			SchemaVersion: jsonout.Version,
			Target:        targetLabel,
			ChangedFiles:  changed,
			Notes:         notes,
			Files:         fileEntries,
		})
	}
	printReviewText(w, targetLabel, changed, notes, fileEntries)
	return nil
}

func gitReviewFiles(repoRoot, rev string) (string, []string, error) {
	if rev == "" {
		// Uncommitted tracked changes first; fall back to HEAD when clean.
		out, err := gitRun(repoRoot, "status", "--porcelain", "--untracked-files=no")
		if err == nil {
			files := parsePorcelainPaths(out)
			if len(files) > 0 {
				return "working-tree", files, nil
			}
		}
		rev = "HEAD"
	}
	if strings.Contains(rev, "..") {
		out, err := gitRun(repoRoot, "diff", "--name-only", rev, "--")
		if err != nil {
			if msg := gitStderrLine(err); msg != "" {
				return "", nil, fmt.Errorf("review %s: %s", rev, msg)
			}
			return "", nil, fmt.Errorf("review: unknown revision or range %q", rev)
		}
		return rev, splitNonEmptyLines(out), nil
	}
	shaOut, err := gitRun(repoRoot, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil || strings.TrimSpace(shaOut) == "" {
		if msg := gitStderrLine(err); msg != "" {
			return "", nil, fmt.Errorf("review %s: %s", rev, msg)
		}
		return "", nil, fmt.Errorf("review: unknown revision %q", rev)
	}
	sha := strings.TrimSpace(shaOut)
	out, showErr := gitRun(repoRoot, "show", "--pretty=format:", "--name-only", sha)
	if showErr != nil {
		return shortSHA(sha), nil, nil //nolint:nilerr // fall back to empty file list if git show fails
	}
	return shortSHA(sha), splitNonEmptyLines(out), nil
}

func parsePorcelainPaths(out string) []string {
	seen := map[string]bool{}
	var files []string
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if _, after, ok := strings.Cut(p, " -> "); ok {
			p = strings.TrimSpace(after)
		}
		p = strings.Trim(p, `"`)
		p = filepath.ToSlash(p)
		if p != "" && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files
}

func splitNonEmptyLines(out string) []string {
	seen := map[string]bool{}
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		s := filepath.ToSlash(strings.TrimSpace(line))
		if s != "" && !seen[s] {
			seen[s] = true
			lines = append(lines, s)
		}
	}
	sort.Strings(lines)
	return lines
}

func collectReviewNotes(repoRoot string, scope, changed []string) []reviewNoteJSON {
	type fileToken struct {
		rel  string
		base string
		stem string
	}
	tokens := make([]fileToken, 0, len(changed))
	for _, f := range changed {
		base := strings.ToLower(filepath.Base(f))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		tokens = append(tokens, fileToken{
			rel:  strings.ToLower(f),
			base: base,
			stem: stem,
		})
	}

	var notes []reviewNoteJSON
	seen := map[string]bool{}
	addSessions := func(ss []model.Session, defaultOrigin string) {
		for _, s := range ss {
			inScope := len(scope) == 0 || howProjectMatches(s.Project, scope) || defaultOrigin == "repo"
			if !inScope {
				continue
			}
			origin := defaultOrigin
			if s.From == "repo" || strings.HasPrefix(s.Project, model.RepoOriginPrefix) {
				origin = "repo"
			}
			for _, m := range s.Messages {
				text := strings.TrimSpace(m.Text)
				if text == "" {
					continue
				}
				lower := strings.ToLower(text)
				matchedFile := len(tokens) == 0
				for _, tk := range tokens {
					if (tk.rel != "" && strings.Contains(lower, tk.rel)) ||
						(len(tk.base) >= 3 && strings.Contains(lower, tk.base)) ||
						(len(tk.stem) >= 4 && strings.Contains(lower, tk.stem)) {
						matchedFile = true
						break
					}
				}
				if !matchedFile && defaultOrigin != "repo" {
					continue
				}
				key := origin + "\x00" + text
				if seen[key] {
					continue
				}
				seen[key] = true
				notes = append(notes, reviewNoteJSON{
					ID:      s.ID,
					Project: search.SafeLine(s.Project),
					Origin:  origin,
					Text:    search.SafeLine(squeezeTrim(text, 240)),
				})
			}
		}
	}
	addFrom := func(path, defaultOrigin string) {
		if path == "" {
			return
		}
		ss, err := sources.ParseNotesFile(path)
		if err != nil {
			return
		}
		addSessions(ss, defaultOrigin)
	}

	if repoRoot != "" {
		if ss, err := sources.ParseRepoMemoryFile(sources.RepoMemoryPath(repoRoot)); err == nil {
			addSessions(ss, "repo")
		}
	}
	if rp := sources.RepoNotesFile(repoRoot); rp != "" {
		addFrom(rp, "repo")
	}
	if np := sources.NotesFile(); np != "" && np != sources.RepoNotesFile(repoRoot) {
		addFrom(np, "local")
	}
	return notes
}

func printReviewText(w io.Writer, target string, changed []string, notes []reviewNoteJSON, files []reviewFileJSON) {
	if len(changed) == 0 {
		fmt.Fprintf(w, "review %s: no changed files\n", target)
		return
	}
	fmt.Fprintf(w, "review %s (%d changed file%s)\n", target, len(changed), pluralS(len(changed)))
	if len(notes) > 0 {
		fmt.Fprintln(w, "\nNotes:")
		for _, n := range notes {
			tag := n.Origin
			if tag == "" {
				tag = "local"
			}
			fmt.Fprintf(w, "  [%s] %s\n", tag, n.Text)
		}
	}
	printedFiles := 0
	for _, f := range files {
		if len(f.Sessions) == 0 {
			continue
		}
		if printedFiles == 0 {
			fmt.Fprintln(w, "\nPrior sessions:")
		}
		printedFiles++
		fmt.Fprintf(w, "  %s\n", f.Path)
		for _, s := range f.Sessions {
			state := ""
			if s.State != "" {
				state = " [" + s.State + "]"
			}
			line := s.Summary
			if s.Note != "" {
				line = s.Note
			}
			if line != "" {
				fmt.Fprintf(w, "    [%s] %s · %s%s — %s\n", s.Harness, s.Date, s.ID, state, line)
			} else {
				fmt.Fprintf(w, "    [%s] %s · %s%s\n", s.Harness, s.Date, s.ID, state)
			}
		}
	}
	if len(notes) == 0 && printedFiles == 0 {
		fmt.Fprintln(w, "  no prior sessions or settled notes matched these files")
	}
}
