package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja blame <path>:<line>` answers why a line is the way it is.
//
// git says who changed it and when; the session that changed it says what was
// tried first and why the shape was unavoidable. The hard part is not the
// plumbing, it is attribution: a session whose span merely contains the commit
// time and that touched the same file is the wrong session more often than
// anyone would accept. Measured over 494 commits and 4 dependency bumps from a
// month of this repository (#1181):
//
//	rule                                   one session  several  none  bumps wrongly attributed
//	time and file overlap                       378         7     109         2 of 4
//	the session replaced the text the commit deleted 116     0     378         0 of 4
//	the session names the number it closes      355         0     139         2 of 4
//
// So this takes the middle rule and nothing else: the session has to have
// replaced the same text the commit shows as deleted. An edit record holds
// what an edit replaced, so a match means that session performed this very
// change — which makes it the author of the line being read, and the text it
// replaced is what stood there before. It answers for about a quarter
// of commits and says nothing for the rest, which is the trade the issue asks
// for — a rationale invented for a line nobody reasoned about is worse than no
// rationale. The number-closed signal is out for a reason that only shows up in
// the control: the maintainer's own session names the PR number while merging
// it, and that session edited the files too.
type lineCommit struct {
	SHA     string
	Subject string
	When    time.Time
	Author  string
}

// gitLineCommit asks git which commit last wrote a line. The second return is
// why it could not, in the reader's words: a line asked about and then not
// mentioned at all leaves them unable to tell "this line has no history" from
// "your `:120` was ignored" (#3726).
func gitLineCommit(path string, line int) (lineCommit, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return lineCommit{}, "git is not installed, so nothing can say which commit wrote this line"
	}
	if fi, err := os.Stat(path); err != nil {
		return lineCommit{}, "there is no such file here"
	} else if fi.IsDir() {
		return lineCommit{}, "that is a directory, not a file"
	}
	dir := filepath.Dir(path)
	out, err := gitRun(dir, "blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", "--", path)
	if err != nil || strings.TrimSpace(out) == "" {
		return lineCommit{}, lineBlameRefusal(dir, path, line)
	}
	var c lineCommit
	for i, ln := range strings.Split(out, "\n") {
		switch {
		case i == 0:
			head, _, _ := strings.Cut(ln, " ")
			if len(head) < 7 || strings.HasPrefix(head, "0000000") {
				// An uncommitted line has the null sha: there is no commit to
				// attribute, and the reader is looking at their own edit.
				return lineCommit{}, "this line is not committed yet"
			}
			c.SHA = head
		case strings.HasPrefix(ln, "summary "):
			c.Subject = strings.TrimPrefix(ln, "summary ")
		case strings.HasPrefix(ln, "author "):
			c.Author = strings.TrimPrefix(ln, "author ")
		case strings.HasPrefix(ln, "author-time "):
			if sec, err := strconv.ParseInt(strings.TrimPrefix(ln, "author-time "), 10, 64); err == nil {
				c.When = time.Unix(sec, 0)
			}
		}
	}
	if c.SHA == "" {
		return lineCommit{}, "git named no commit for this line"
	}
	return c, ""
}

// lineBlameRefusal turns git's own refusal into the one sentence a reader
// needs. The common one is a line past the end of the file, and the count is
// what makes it actionable.
func lineBlameRefusal(dir, path string, line int) string {
	if n, err := countLines(path); err == nil && line > n {
		return fmt.Sprintf("the file has %d line%s", n, pluralS(n))
	}
	if out, err := gitRun(dir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return "this file is not in a repository"
	}
	if out, err := gitRun(dir, "ls-files", "--error-unmatch", "--", path); err != nil || strings.TrimSpace(out) == "" {
		return "this file is not tracked, so no commit wrote the line"
	}
	return "git could not say which commit wrote this line"
}

// countLines is how many lines a file has, for the refusal above.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64*1024)
	n := 0
	for {
		read, err := f.Read(buf)
		for _, b := range buf[:read] {
			if b == '\n' {
				n++
			}
		}
		if err != nil {
			return n, nil
		}
	}
}

// gitRemovedLines is what a commit deleted, normalised the way a recorded edit
// span will be. Short lines are left out: `}` and `return nil` are in every
// diff and match every session.
func gitRemovedLines(path, sha string) map[string]bool {
	out, err := gitRun(filepath.Dir(path), "show", "--unified=0", "--format=", sha)
	if err != nil {
		return nil
	}
	removed := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ln, "-") || strings.HasPrefix(ln, "---") {
			continue
		}
		if body := blameSpanKey(strings.TrimPrefix(ln, "-")); body != "" {
			removed[body] = true
		}
	}
	return removed
}

// blameSpanKey is the form a diff line and a recorded edit span are compared
// in. The indexer hashes the written side through the same function, because a
// normalisation that differs by one space attributes nothing and reads as a
// ranking problem.
func blameSpanKey(s string) string { return sources.WrittenLineKey(s) }

// gitRun is one git call, bounded. git is optional for deja and a repository
// can be enormous; a caller that cannot answer says nothing.
func gitRun(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), blameGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

const blameGitTimeout = 5 * time.Second

// lineAuthor is the session that wrote the line, with the line of its own that
// says why.
type lineAuthor struct {
	Session model.Session
	Matched string // the text this session replaced, or the line it wrote
	Asked   string // what the session was asked to do, which is its own title
	// Wrote distinguishes the two rules. Replacing the text a commit deleted
	// proves the session made that very change; having written the line, in
	// this file, before the commit is weaker — a line written in twenty
	// sessions attributes to the last of them rather than to all.
	Wrote bool
	// Said is what the session said in the turn this edit sits in, which is
	// the closest thing to a reason the transcript holds — see saidBefore.
	Said string
}

// saidBefore is what the session said in the turn the edit sits in: the
// nearest thing it wrote before the record, in its own words.
//
// It is not called the reason, because it is not always one. #3722 measured
// the session's conclusion against the change and found no overlap at all — 0
// of 81 — and concluded no single line lifted out of a session is the why. The
// turn holding the edit is a different question and a better one. Measured
// over 41 attributed lines on this repository, the turn immediately before the
// record was there for 40 of them, and read by eye 16 of 33 distinct turns
// carried an actual reason: a finding, a constraint, a diagnosis. The other
// half say what is about to be done and nothing about why ("now the shared
// writer"). The lexical test #3722 used — the file name, or two content words
// of the commit subject — caught 1 of those 40, so it is the wrong bar: a
// reason written in another language than the commit subject shares no words
// with it (#3723).
//
// So the output says exactly what this is, and the floor below drops the
// shortest half, which is where the pure narration sits.
func saidBefore(s model.Session, at int) string {
	for i := at - 1; i >= 0 && i > at-saidBeforeLookback; i-- {
		m := s.Messages[i]
		if m.Role != "assistant" && m.Role != "developer" {
			continue
		}
		// deja's own output must not come back as the reason for a line: a
		// session that ran deja keeps what it printed, and this reads the
		// messages around an edit rather than the ones blame already filters
		// (#1330, #3723).
		text := strings.Join(strings.Fields(search.WithoutOwnReport(m.Text)), " ")
		r := []rune(text)
		if len(r) < saidBeforeFloor {
			continue
		}
		if len(r) > saidBeforeMax {
			return string(r[:saidBeforeMax]) + "…"
		}
		return text
	}
	return ""
}

const (
	// saidBeforeLookback bounds how far back the turn can be. A record sits
	// among the tool calls of its own turn, and beyond a few dozen messages
	// the text belongs to earlier work.
	saidBeforeLookback = 40
	// saidBeforeFloor is the length below which a turn is a handover line
	// rather than a reason. Of the 16 turns that carried no reason in the
	// measurement above, 9 are shorter than this; of the 16 that carried one,
	// none is.
	saidBeforeFloor = 60
	saidBeforeMax   = 220
)

// saidBeforePrefix labels the line for what it is, in both renderings, and is
// what the own-output recogniser in internal/search keys on.
const saidBeforePrefix = "said just before this edit: "

// attributeLine picks the session that replaced the text the commit deleted —
// the session that made this change, and so wrote the line being read. Failing
// that, the session that wrote this very line into this very file before the
// commit: 71% of commits delete nothing at all, so the replaced side is silent
// about most lines in a repository, and a line a commit added has no replaced
// text for any rule to match (#3773). Nothing when neither has evidence, which
// is the answer for a dependency bump, a merge, and any commit deja never saw.
func attributeLine(sessions []model.Session, target search.BlameTarget, c lineCommit, removed map[string]bool) (lineAuthor, bool) {
	if a, ok := attributeByReplaced(sessions, target, c, removed); ok {
		return a, true
	}
	return attributeByWritten(sessions, target, c)
}

// attributeByWritten is the weaker rule: a session that wrote this line, into
// this file, before the commit that carried it.
func attributeByWritten(sessions []model.Session, target search.BlameTarget, c lineCommit) (lineAuthor, bool) {
	line := fileLine(target.FullPath, target.Line)
	want, ok := sources.HashWrittenLine(line)
	if !ok {
		// Too short to be evidence: `}` and `return nil` were written by every
		// session, so a match would say nothing about this one.
		return lineAuthor{}, false
	}
	best := lineAuthor{}
	var bestAt time.Time
	for _, s := range sessions {
		for i, m := range s.Messages {
			if m.Role != sources.RoleWrote {
				continue
			}
			path, has := sources.WroteRecordHas(m.Text, want)
			if !has || !recordNamesFile(path, target) {
				continue
			}
			// A session that wrote it after the commit wrote something else:
			// the same line arrived at again, later.
			if !m.Time.IsZero() && !c.When.IsZero() && m.Time.After(c.When) {
				continue
			}
			if best.Matched == "" || m.Time.After(bestAt) {
				best = lineAuthor{
					Session: s,
					Matched: blameSpanKey(line),
					Asked:   search.SessionTitle(s),
					Wrote:   true,
					Said:    saidBefore(s, i),
				}
				bestAt = m.Time
			}
		}
	}
	return best, best.Matched != ""
}

// fileLine reads one line of a file, which is the text git attributed to the
// commit.
func fileLine(path string, n int) string {
	if n <= 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	// A generated file can hold a line longer than the scanner's default 64 KB,
	// and stopping there would answer about the wrong line.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for i := 1; sc.Scan(); i++ {
		if i == n {
			return sc.Text()
		}
	}
	return ""
}

func attributeByReplaced(sessions []model.Session, target search.BlameTarget, c lineCommit, removed map[string]bool) (lineAuthor, bool) {
	if len(removed) == 0 {
		return lineAuthor{}, false
	}
	best := lineAuthor{}
	var bestAt time.Time
	for _, s := range sessions {
		for i, m := range s.Messages {
			if m.Role != sources.RoleEdit {
				continue
			}
			// An edit record is "path\n<the text that was replaced>".
			path, span, ok := strings.Cut(m.Text, "\n")
			if !ok || !recordNamesFile(path, target) {
				continue
			}
			// After the commit it cannot be what the commit removed.
			if !m.Time.IsZero() && !c.When.IsZero() && m.Time.After(c.When) {
				continue
			}
			for _, ln := range strings.Split(span, "\n") {
				key := blameSpanKey(ln)
				if key == "" || !removed[key] {
					continue
				}
				// Two sessions can have written the same line — on another
				// repository on the same store, 3 of 17 attributed commits had
				// a second candidate. The last one to write it before the
				// commit is the one the commit carried (#3723).
				if best.Matched == "" || m.Time.After(bestAt) {
					best = lineAuthor{Session: s, Matched: key, Asked: search.SessionTitle(s), Said: saidBefore(s, i)}
					bestAt = m.Time
				}
				break
			}
		}
	}
	return best, best.Matched != ""
}

// recordNamesFile reports whether a recorded path names the file asked about. The
// recorded one is absolute and may come from a worktree or another machine, so
// the comparison is on the tail.
func recordNamesFile(recorded string, target search.BlameTarget) bool {
	recorded = strings.TrimSpace(strings.ReplaceAll(recorded, "\\", "/"))
	if recorded == "" {
		return false
	}
	want := filepath.ToSlash(target.FullPath)
	if recorded == want {
		return true
	}
	// A worktree or a peer's checkout: the repository-relative tail is what
	// both have in common. Two segments is enough to tell `pool.go` in two
	// packages apart and short enough to survive a different checkout root.
	return strings.HasSuffix(recorded, "/"+tailSegments(want, 2)) && strings.HasSuffix(want, "/"+tailSegments(recorded, 2))
}

func tailSegments(p string, n int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) <= n {
		return strings.Join(parts, "/")
	}
	return strings.Join(parts[len(parts)-n:], "/")
}

// No "why" line is printed, and that is a measured decision rather than an
// omission.
//
// The obvious one — quote what the session settled — was tried both ways over
// 81 attributed lines on a real store: the session's own conclusion overlapped
// the change (its file name, or two content words of the commit subject) 0
// times, and the first conclusion the session reached after writing the line 2
// times. A session runs for hours and settles a dozen things, so any single
// line lifted out of it reads as the reason for a change it has nothing to do
// with — an invented rationale, which is the failure this whole surface exists
// to avoid. What deja can say with evidence is which session replaced the text
// that was there, and where to read it (#1181).

// printLineAuthor writes the line-level answer above the ordinary listing.
func printLineAuthor(w io.Writer, target search.BlameTarget, c lineCommit, a lineAuthor, found bool) {
	when := ""
	if !c.When.IsZero() {
		when = " · " + c.When.Local().Format("2006-01-02")
	}
	fmt.Fprintf(w, "%s:%d last changed in %s%s · %s\n", target.Base, target.Line, shortSHA(c.SHA), when, firstLine(c.Subject))
	if !found {
		// Silence is the honest answer, and it has to say which silence: deja
		// holds no session that wrote this line, rather than deja having
		// nothing about the file at all.
		fmt.Fprintf(w, "  no indexed session wrote this line or the lines this commit replaced — nothing to say about this line\n")
		return
	}
	s := a.Session
	fmt.Fprintf(w, "  written in %s · %s · %s\n", search.SafeLine(s.Harness), shortID(s.ID), search.SafeLine(s.Project))
	// Which rule answered, because the two are not equally strong and a reader
	// deciding whether to trust the attribution needs to know which they have.
	if a.Wrote {
		fmt.Fprintf(w, "  wrote this line: %s\n", search.SafeLine(trunc80(a.Matched)))
	} else {
		fmt.Fprintf(w, "  replaced: %s\n", search.SafeLine(trunc80(a.Matched)))
	}
	if a.Asked != "" {
		fmt.Fprintf(w, "  asked: %s\n", search.SafeLine(trunc80(a.Asked)))
	}
	if a.Said != "" {
		fmt.Fprintf(w, "  %s%s\n", saidBeforePrefix, search.SafeLine(a.Said))
	}
	fmt.Fprintf(w, "  why, in full: deja ctx %s\n", shortID(s.ID))
}

func trunc80(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// lineBlame is the whole line-level answer: the commit that last wrote the
// line, and the session that wrote what that commit replaced. Quiet when git
// is not there, when the file is not in a repository, or when the line is
// uncommitted — none of those is a fact about the store.
func lineBlame(w io.Writer, dir string, target search.BlameTarget, hits []search.BlameHit) {
	c, author, found, why := lineAttribution(dir, target, hits)
	if why != "" {
		fmt.Fprintf(w, "%s:%d — %s\n\n", target.Base, target.Line, why)
		return
	}
	printLineAuthor(w, target, c, author, found)
	fmt.Fprintln(w)
}

// lineAttribution is the whole line-level answer as data: the commit git names,
// the session the rules attribute it to, and — when nothing can be said at all
// — the sentence saying which silence this is. The prose answer and the JSON
// one are two renderings of this, so neither can drift from the other (#3723).
func lineAttribution(dir string, target search.BlameTarget, hits []search.BlameHit) (lineCommit, lineAuthor, bool, string) {
	c, why := gitLineCommit(target.FullPath, target.Line)
	if why != "" {
		return lineCommit{}, lineAuthor{}, false, why
	}
	// A blame hit carries only the messages that mention the file, and an edit
	// record is not one of them — the span is what was replaced, which does not
	// have to name the path. So the candidates are read back whole, in the one
	// pass FindManyByIdentity makes (#1069).
	ids := make([]index.Identity, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, index.Identity{Harness: h.Session.Harness, ID: h.Session.ID})
	}
	sessions, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return c, lineAuthor{}, false, ""
	}
	author, found := attributeLine(sessions, target, c, gitRemovedLines(target.FullPath, c.SHA))
	return c, author, found, ""
}
