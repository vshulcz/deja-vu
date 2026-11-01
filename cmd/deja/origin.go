package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/harnesscolor"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// `deja origin [rev|range]` answers which agent session produced a commit or a
// range of commits, by matching the lines each commit removed and added against
// the recorded edit spans and written-line hashes in the index (#534).
//
// Attribution is three-valued:
//   - "attributed": one session wrote the commit's changes (and no second
//     session came within 75% of the leader's matched file count, and the write
//     landed within 7 days before the commit).
//   - "uncertain": two sessions overlap within a quarter of the leader, or the
//     matching write happened more than 7 days before the commit (a line written
//     long ago and committed later).
//   - "unknown": no indexed session matched any changed file.
type originOptions struct {
	Rev     string
	Harness string
	Project string
	Limit   int
	JSON    bool
}

const (
	originDefaultLimit     = 20
	originMaxLimit         = 100
	originMaxStaleGap      = 7 * 24 * time.Hour
	originStatusAttributed = "attributed"
	originStatusUncertain  = "uncertain"
	originStatusUnknown    = "unknown"
)

type originEnvelopeJSON struct {
	SchemaVersion int                `json:"schema_version"`
	Commits       []originCommitJSON `json:"commits"`
}

type originCommitJSON struct {
	SHA        string              `json:"sha"`
	Subject    string              `json:"subject,omitempty"`
	Author     string              `json:"author,omitempty"`
	When       string              `json:"when,omitempty"`
	Status     string              `json:"status"`
	Attributed bool                `json:"attributed"`
	Uncertain  bool                `json:"uncertain,omitempty"`
	Rule       string              `json:"rule,omitempty"`
	Files      []string            `json:"files,omitempty"`
	Matched    string              `json:"matched,omitempty"`
	SaidBefore string              `json:"said_before,omitempty"`
	Prompt     string              `json:"prompt,omitempty"`
	TurnGap    string              `json:"turn_gap,omitempty"`
	WriteGap   string              `json:"write_gap,omitempty"`
	Session    *originSessionJSON  `json:"session,omitempty"`
	Candidates []originSessionJSON `json:"candidates,omitempty"`
	Why        string              `json:"why,omitempty"`
}

type originSessionJSON struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Asked   string `json:"asked,omitempty"`
	Ctx     string `json:"ctx"`
}

type originCommit struct {
	SHA     string
	Subject string
	Author  string
	When    time.Time
	Patches []originFilePatch
}

type originFilePatch struct {
	Path    string
	Removed map[string]bool
	Added   map[string]string // hash -> raw line
}

type originVote struct {
	session   model.Session
	files     map[string]bool
	replaced  int
	wrote     int
	matched   string
	said      string
	prompt    string
	turnGap   time.Duration
	lastWrite time.Time
}

func parseOrigin(args []string) (originOptions, error) {
	o := originOptions{Limit: originDefaultLimit}
	afterDashDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			if o.Rev != "" {
				return o, errors.New("origin accepts one revision or range")
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
				if perr != nil || n < 1 || n > originMaxLimit {
					return o, fmt.Errorf("--limit must be 1..%d", originMaxLimit)
				}
				o.Limit = n
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("origin: unknown flag %q; a revision that starts with a dash goes after `--`", a)
			}
			if o.Rev != "" {
				return o, errors.New("origin accepts one revision or range")
			}
			o.Rev = a
		}
	}
	if o.Rev == "" {
		o.Rev = "HEAD"
	}
	return o, nil
}

func runOrigin(dir string, args []string, w io.Writer) error {
	o, err := parseOrigin(args)
	if err != nil {
		return err
	}
	if herr := checkHarness(&o.Harness); herr != nil {
		return herr
	}
	cwd := howCwd()
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return errors.New("origin: git is not installed")
	}
	rootOut, err := gitRun(cwd, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(rootOut) == "" {
		return errors.New("origin: not in a git repository")
	}
	repoRoot := strings.TrimSpace(rootOut)

	commits, err := gitOriginCommits(repoRoot, o.Rev, o.Limit)
	if err != nil {
		return err
	}
	if ierr := index.Ensure(dir, o.Harness, false, os.Stderr); ierr != nil {
		return ensureError(dir, ierr)
	}

	sessions := loadOriginCandidateSessions(dir, repoRoot, commits, o)
	results := make([]originCommitJSON, 0, len(commits))
	for _, c := range commits {
		results = append(results, attributeOriginCommit(repoRoot, c, sessions))
	}

	if o.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(originEnvelopeJSON{
			SchemaVersion: jsonout.Version,
			Commits:       results,
		})
	}
	printOriginText(w, results)
	return nil
}

func gitOriginCommits(repoRoot, rev string, limit int) ([]originCommit, error) {
	isRange := strings.Contains(rev, "..")
	var shas []string
	if isRange {
		out, err := gitRun(repoRoot, "rev-list", fmt.Sprintf("--max-count=%d", limit), rev, "--")
		if err != nil {
			if msg := gitStderrLine(err); msg != "" {
				return nil, fmt.Errorf("origin %s: %s", rev, msg)
			}
			return nil, fmt.Errorf("origin: unknown revision or range %q", rev)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				shas = append(shas, s)
			}
		}
	} else {
		out, err := gitRun(repoRoot, "rev-parse", "--verify", rev+"^{commit}")
		if err != nil || strings.TrimSpace(out) == "" {
			if msg := gitStderrLine(err); msg != "" {
				return nil, fmt.Errorf("origin %s: %s", rev, msg)
			}
			return nil, fmt.Errorf("origin: unknown revision %q", rev)
		}
		shas = []string{strings.TrimSpace(out)}
	}

	commits := make([]originCommit, 0, len(shas))
	for _, sha := range shas {
		c, err := gitOriginOneCommit(repoRoot, sha)
		if err != nil {
			continue
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func gitOriginOneCommit(repoRoot, sha string) (originCommit, error) {
	meta, err := gitRun(repoRoot, "show", "-s", "--format=%H%n%an%n%at%n%s", sha)
	if err != nil {
		return originCommit{}, err
	}
	lines := strings.SplitN(strings.TrimRight(meta, "\n"), "\n", 4)
	if len(lines) < 4 {
		return originCommit{}, fmt.Errorf("short git show output for %s", sha)
	}
	var when time.Time
	if sec, err := strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64); err == nil {
		when = time.Unix(sec, 0)
	}
	diffOut, _ := gitRun(repoRoot, "show", "--unified=0", "--format=", sha)
	return originCommit{
		SHA:     strings.TrimSpace(lines[0]),
		Author:  strings.TrimSpace(lines[1]),
		When:    when,
		Subject: strings.TrimSpace(lines[3]),
		Patches: parseOriginDiff(diffOut),
	}, nil
}

func parseOriginDiff(diff string) []originFilePatch {
	var patches []originFilePatch
	var cur *originFilePatch
	flush := func() {
		if cur != nil && cur.Path != "" && (len(cur.Removed) > 0 || len(cur.Added) > 0) {
			patches = append(patches, *cur)
		}
		cur = nil
	}
	for ln := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			cur = &originFilePatch{
				Removed: map[string]bool{},
				Added:   map[string]string{},
			}
		case cur == nil:
			continue
		case strings.HasPrefix(ln, "+++ b/"):
			cur.Path = strings.TrimSpace(strings.TrimPrefix(ln, "+++ b/"))
		case strings.HasPrefix(ln, "--- a/") && cur.Path == "":
			cur.Path = strings.TrimSpace(strings.TrimPrefix(ln, "--- a/"))
		case strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---"):
			if key := blameSpanKey(strings.TrimPrefix(ln, "-")); key != "" {
				cur.Removed[key] = true
			}
		case strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++"):
			raw := strings.TrimPrefix(ln, "+")
			if h, ok := sources.HashWrittenLine(raw); ok {
				hk := strconv.FormatUint(h, 16)
				if _, exists := cur.Added[hk]; !exists {
					cur.Added[hk] = blameSpanKey(raw)
				}
			}
		}
	}
	flush()
	return patches
}

func loadOriginCandidateSessions(dir, repoRoot string, commits []originCommit, o originOptions) []model.Session {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return nil
	}
	bases := map[string]bool{}
	for _, c := range commits {
		for _, p := range c.Patches {
			bases[strings.ToLower(filepath.Base(filepath.FromSlash(p.Path)))] = true
		}
	}
	if len(bases) == 0 {
		return nil
	}
	scope := howScope(repoRoot, o.Project, false)
	pol := policy.Load()
	sort.Slice(metas, func(i, j int) bool { return newestFirst(metas[i], metas[j]) })

	var want []index.Identity
	for _, meta := range metas {
		if len(want) >= blameToucherCap*2 {
			break
		}
		if o.Harness != "" && !strings.EqualFold(meta.Harness, o.Harness) {
			continue
		}
		if len(scope) > 0 && !howProjectMatches(meta.Project, scope) {
			continue
		}
		if !pol.Allows(policy.ActivationSearch, meta.Project) || pol.Ignored(meta.Path, meta.Project) {
			continue
		}
		for _, p := range meta.Touched {
			if bases[strings.ToLower(filepath.Base(filepath.FromSlash(p)))] {
				want = append(want, index.Identity{Harness: meta.Harness, ID: meta.ID})
				break
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	ss, err := index.FindManyByIdentity(dir, want)
	if err != nil {
		return nil
	}
	sourceInstance := os.Getenv("DEJA_SOURCE_INSTANCE")
	for i := range ss {
		ss[i].SetSource(sourceInstance)
	}
	return ss
}

func attributeOriginCommit(repoRoot string, c originCommit, sessions []model.Session) originCommitJSON {
	out := originCommitJSON{
		SHA:     c.SHA,
		Subject: search.SafeLine(firstLine(c.Subject)),
		Author:  search.SafeLine(c.Author),
		Status:  originStatusUnknown,
	}
	if !c.When.IsZero() {
		out.When = c.When.UTC().Format(time.RFC3339)
	}
	if len(c.Patches) == 0 || len(sessions) == 0 {
		out.Why = "no indexed session wrote the lines this commit changed"
		return out
	}

	votes := map[string]*originVote{}
	voteFor := func(s model.Session) *originVote {
		k := s.Harness + ":" + s.ID
		v, ok := votes[k]
		if !ok {
			v = &originVote{session: s, files: map[string]bool{}}
			votes[k] = v
		}
		return v
	}

	for _, p := range c.Patches {
		full := filepath.Join(repoRoot, filepath.FromSlash(p.Path))
		target := search.BlameTarget{
			FullPath: full,
			Base:     filepath.Base(full),
		}
		s, matched, msgIdx, msgTime, wrote, ok := matchOriginPatch(sessions, target, p, c.When)
		if !ok {
			continue
		}
		v := voteFor(s)
		v.files[p.Path] = true
		if wrote {
			v.wrote++
		} else {
			v.replaced++
		}
		if v.matched == "" || msgTime.After(v.lastWrite) {
			v.matched = matched
			v.lastWrite = msgTime
			v.said = saidBefore(s, msgIdx)
			v.prompt, v.turnGap = originUserTurnBefore(s, msgIdx, msgTime)
		}
	}

	if len(votes) == 0 {
		out.Why = "no indexed session wrote the lines this commit changed"
		return out
	}

	ranked := make([]*originVote, 0, len(votes))
	for _, v := range votes {
		ranked = append(ranked, v)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if len(a.files) != len(b.files) {
			return len(a.files) > len(b.files)
		}
		if a.replaced != b.replaced {
			return a.replaced > b.replaced
		}
		if !a.lastWrite.Equal(b.lastWrite) {
			return a.lastWrite.After(b.lastWrite)
		}
		return a.session.ID < b.session.ID
	})

	leader := ranked[0]
	files := make([]string, 0, len(leader.files))
	for f := range leader.files {
		files = append(files, search.SafeLine(f))
	}
	sort.Strings(files)
	out.Files = files
	out.Rule = lineRuleReplaced
	if leader.replaced == 0 {
		out.Rule = lineRuleWrote
	}
	out.Matched = search.SafeLine(leader.matched)
	out.SaidBefore = search.SafeLine(leader.said)
	if leader.prompt != "" {
		out.Prompt = search.SafeLine(leader.prompt)
	}
	if leader.turnGap > 0 {
		out.TurnGap = formatOriginGap(leader.turnGap)
	}
	var writeGap time.Duration
	if !c.When.IsZero() && !leader.lastWrite.IsZero() && c.When.After(leader.lastWrite) {
		writeGap = c.When.Sub(leader.lastWrite)
		out.WriteGap = formatOriginGap(writeGap)
	}

	// Refuse to claim certainty when a second session is within a quarter of
	// the leader's matched file count (#534).
	if len(ranked) > 1 && len(ranked[1].files)*4 >= len(leader.files)*3 {
		out.Status = originStatusUncertain
		out.Uncertain = true
		out.Why = fmt.Sprintf("two sessions matched overlapping files in this commit (%s %s and %s %s)",
			ranked[0].session.Harness, search.ShortID(ranked[0].session.ID),
			ranked[1].session.Harness, search.ShortID(ranked[1].session.ID))
		out.Candidates = []originSessionJSON{
			toOriginSessionJSON(ranked[0].session),
			toOriginSessionJSON(ranked[1].session),
		}
		return out
	}

	// A line written more than a week before the commit is an old edit that
	// landed later, not this commit's session (#534).
	if writeGap > originMaxStaleGap {
		out.Status = originStatusUncertain
		out.Uncertain = true
		sess := toOriginSessionJSON(leader.session)
		out.Session = &sess
		out.Why = fmt.Sprintf("last matching write was %s before the commit", out.WriteGap)
		return out
	}

	out.Status = originStatusAttributed
	out.Attributed = true
	sess := toOriginSessionJSON(leader.session)
	out.Session = &sess
	return out
}

func toOriginSessionJSON(s model.Session) originSessionJSON {
	return originSessionJSON{
		Harness: search.SafeLine(s.Harness),
		ID:      s.ID,
		Project: search.SafeLine(s.Project),
		Asked:   search.SafeLine(search.SessionTitle(s)),
		Ctx:     "deja ctx " + search.ShortID(s.ID),
	}
}

func matchOriginPatch(sessions []model.Session, target search.BlameTarget, p originFilePatch, commitWhen time.Time) (model.Session, string, int, time.Time, bool, bool) {
	if len(p.Removed) > 0 {
		var bestS model.Session
		var bestMatched string
		var bestIdx int
		var bestAt time.Time
		for _, s := range sessions {
			for i, m := range s.Messages {
				if m.Role != sources.RoleEdit {
					continue
				}
				path, span, ok := strings.Cut(m.Text, "\n")
				if !ok || !search.RecordNamesFile(path, target) {
					continue
				}
				if !m.Time.IsZero() && !commitWhen.IsZero() && m.Time.After(commitWhen) {
					continue
				}
				for ln := range strings.SplitSeq(span, "\n") {
					key := blameSpanKey(ln)
					if key == "" || !p.Removed[key] {
						continue
					}
					if bestMatched == "" || m.Time.After(bestAt) || (m.Time.Equal(bestAt) && search.SourceOf(sessions, s, bestS, m.Time)) {
						bestS, bestMatched, bestIdx, bestAt = s, key, i, m.Time
					}
					break
				}
			}
		}
		if bestMatched != "" {
			return bestS, bestMatched, bestIdx, bestAt, false, true
		}
	}

	if len(p.Added) > 0 {
		var bestS model.Session
		var bestMatched string
		var bestIdx int
		var bestAt time.Time
		for _, s := range sessions {
			for i, m := range s.Messages {
				if m.Role != sources.RoleWrote {
					continue
				}
				if !m.Time.IsZero() && !commitWhen.IsZero() && m.Time.After(commitWhen) {
					continue
				}
				path, rest, ok := strings.Cut(m.Text, "\n")
				if !ok || !search.RecordNamesFile(path, target) {
					continue
				}
				for tok := range strings.FieldsSeq(rest) {
					lineText, ok := p.Added[tok]
					if !ok {
						continue
					}
					if bestMatched == "" || m.Time.After(bestAt) || (m.Time.Equal(bestAt) && search.SourceOf(sessions, s, bestS, m.Time)) {
						bestS, bestMatched, bestIdx, bestAt = s, lineText, i, m.Time
					}
					break
				}
			}
		}
		if bestMatched != "" {
			return bestS, bestMatched, bestIdx, bestAt, true, true
		}
	}

	return model.Session{}, "", 0, time.Time{}, false, false
}

// originUserTurnBefore finds the nearest substantive user turn before msgIdx,
// refusing compaction summaries, agent plumbing, slash commands, and bare
// continuation nudges (#534).
func originUserTurnBefore(s model.Session, at int, writeTime time.Time) (string, time.Duration) {
	for i := at - 1; i >= 0; i-- {
		m := s.Messages[i]
		if m.Role == sources.RoleSummary {
			continue
		}
		if m.Role != "user" {
			continue
		}
		text := strings.TrimSpace(m.Text)
		if text == "" || digest.IsAgentArtifact(text) || digest.IsCompactionSummary(text) || strings.HasPrefix(text, "/") {
			continue
		}
		clean := strings.Join(strings.Fields(search.WithoutOwnReport(text)), " ")
		if digest.IsTrivialContinuation(clean) {
			continue
		}
		var gap time.Duration
		if !writeTime.IsZero() && !m.Time.IsZero() && writeTime.After(m.Time) {
			gap = writeTime.Sub(m.Time)
		}
		r := []rune(clean)
		if len(r) > saidBeforeMax {
			clean = string(r[:saidBeforeMax]) + "…"
		}
		return clean, gap
	}
	return "", 0
}

func formatOriginGap(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(1, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Round(time.Hour).Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func printOriginText(w io.Writer, commits []originCommitJSON) {
	color, width := search.ColorOK(w), printableWidth(w)
	line := func(label, text string) {
		hang := "    "
		out := termwidth.Indent(text, width, "  "+label, hang)
		if color && label != "" {
			out = "  " + statBold + strings.TrimSuffix(label, " ") + statReset + " " + strings.TrimPrefix(out, "  "+label)
		}
		fmt.Fprintln(w, out)
	}
	for idx, c := range commits {
		if idx > 0 {
			fmt.Fprintln(w)
		}
		when := ""
		if c.When != "" {
			if t, err := time.Parse(time.RFC3339, c.When); err == nil {
				when = " · " + search.DisplayDate(t)
			}
		}
		head := fmt.Sprintf("commit %s%s · %s", shortSHA(c.SHA), when, c.Subject)
		head = termwidth.Indent(head, width, "", "  ")
		if color {
			head = statBold + head + statReset
		}
		fmt.Fprintln(w, head)

		switch c.Status {
		case originStatusUnknown:
			line("", c.Why)
		case originStatusUncertain:
			line("uncertain: ", c.Why)
			if c.Session != nil {
				line("closest session: ", fmt.Sprintf("%s · %s · %s (%s)", c.Session.Harness, search.ShortID(c.Session.ID), c.Session.Project, c.Session.Ctx))
			}
		case originStatusAttributed:
			s := c.Session
			harness := s.Harness
			if color {
				harness = harnesscolor.Paint(s.Harness, harness, true)
			}
			gapSuffix := ""
			if c.WriteGap != "" {
				gapSuffix = fmt.Sprintf(" (%s before commit)", c.WriteGap)
			}
			fmt.Fprintf(w, "  written in %s · %s · %s%s\n", harness, search.ShortID(s.ID), s.Project, gapSuffix)
			if len(c.Files) > 0 {
				line("files: ", strings.Join(c.Files, ", "))
			}
			if c.Rule == lineRuleWrote {
				line("wrote this line: ", trunc80(c.Matched))
			} else {
				line("replaced: ", trunc80(c.Matched))
			}
			if c.Prompt != "" {
				label := "user turn before edit: "
				if c.TurnGap != "" {
					label = fmt.Sprintf("user turn (%s before edit): ", c.TurnGap)
				}
				line(label, trunc80(c.Prompt))
			}
			if c.SaidBefore != "" {
				line(saidBeforePrefix, c.SaidBefore)
			}
			line("why, in full: ", s.Ctx)
		}
	}
}
