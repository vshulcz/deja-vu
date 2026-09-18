package main

import (
	"context"
	"fmt"
	"io"
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

// gitLineCommit asks git which commit last wrote a line.
func gitLineCommit(path string, line int) (lineCommit, bool) {
	dir := filepath.Dir(path)
	out, err := gitRun(dir, "blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", "--", path)
	if err != nil || strings.TrimSpace(out) == "" {
		return lineCommit{}, false
	}
	var c lineCommit
	for i, ln := range strings.Split(out, "\n") {
		switch {
		case i == 0:
			head, _, _ := strings.Cut(ln, " ")
			if len(head) < 7 || strings.HasPrefix(head, "0000000") {
				// An uncommitted line has the null sha: there is no commit to
				// attribute, and the reader is looking at their own edit.
				return lineCommit{}, false
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
	return c, c.SHA != ""
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
// in: one space between words, and nothing shorter than a line that could only
// have been written on purpose.
func blameSpanKey(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) < blameSpanMinRunes {
		return ""
	}
	return s
}

// blameSpanMinRunes is where a line stops being evidence. Measured on this
// repository's diffs, below this the matches are braces, `return err` and
// import lines — present in every commit and in every session.
const blameSpanMinRunes = 24

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
	Matched string // the text this session is recorded as having replaced
	Asked   string // what the session was asked to do, which is its own title
}

// attributeLine picks the session that replaced the text the commit deleted —
// the session that made this change, and so wrote the line being read. Nothing when no session did — which is the answer for a dependency
// bump, a merge, and any commit deja never saw.
func attributeLine(sessions []model.Session, target search.BlameTarget, c lineCommit, removed map[string]bool) (lineAuthor, bool) {
	if len(removed) == 0 {
		return lineAuthor{}, false
	}
	best := lineAuthor{}
	var bestAt time.Time
	for _, s := range sessions {
		for _, m := range s.Messages {
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
					best = lineAuthor{Session: s, Matched: key, Asked: search.SessionTitle(s)}
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
		fmt.Fprintf(w, "  no indexed session wrote the lines this commit replaced — nothing to say about this line\n")
		return
	}
	s := a.Session
	fmt.Fprintf(w, "  written in %s · %s · %s\n", search.SafeLine(s.Harness), shortID(s.ID), search.SafeLine(s.Project))
	fmt.Fprintf(w, "  replaced: %s\n", search.SafeLine(trunc80(a.Matched)))
	if a.Asked != "" {
		fmt.Fprintf(w, "  asked: %s\n", search.SafeLine(trunc80(a.Asked)))
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
	c, ok := gitLineCommit(target.FullPath, target.Line)
	if !ok {
		return
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
		return
	}
	author, found := attributeLine(sessions, target, c, gitRemovedLines(target.FullPath, c.SHA))
	printLineAuthor(w, target, c, author, found)
	fmt.Fprintln(w)
}
