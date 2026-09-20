package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The line answer as JSON, which is what an agent asking about a line actually
// wants: today it has to read the prose above blame's array (#3723).
//
// It is not a row in that array and cannot be one. `deja blame --json` is a
// top-level array of the sessions that discussed a file — a documented,
// additive-only shape — and the line answer is about one line and carries a
// commit, a rule and at most one session. So it is its own object behind its
// own flag, the way `deja stats --impact` is: a caller that asks for the line
// answer gets the object, and every existing consumer of the array sees no
// change at all.
//
// One line of output, deliberately, rather than the indented shape the
// envelopes use. Anything deja prints that names a file becomes evidence about
// that file in the next session's transcript, and blame then reads its own
// answer back (#1330, #3722). A single line is one thing for the recogniser in
// internal/search to drop; an indented object is twelve, and the ones carrying
// the path and the matched text are the ones that would survive.
type lineAnswerJSON struct {
	Kind          string           `json:"kind"`
	SchemaVersion int              `json:"schema_version"`
	File          string           `json:"file"`
	Line          int              `json:"line"`
	Commit        *lineCommitJSON  `json:"commit,omitempty"`
	Attributed    bool             `json:"attributed"`
	Rule          string           `json:"rule,omitempty"`
	Matched       string           `json:"matched,omitempty"`
	Session       *lineSessionJSON `json:"session,omitempty"`
	// SaidBefore is the session's own words in the turn the edit sits in. It
	// is not called the reason: about half of these turns carry one and half
	// say what is about to be done, which is why the field, the prose label
	// and the docs all say what it literally is (#3723).
	SaidBefore string `json:"said_before,omitempty"`
	Why        string `json:"why,omitempty"`
}

type lineCommitJSON struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
	Author  string `json:"author,omitempty"`
	When    string `json:"when,omitempty"`
}

type lineSessionJSON struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Asked   string `json:"asked,omitempty"`
	Ctx     string `json:"ctx"`
}

// lineAnswerKind is what the object says it is, and what the recogniser looks
// for. It is the first field so the marker is on the line however long the rest
// runs.
const lineAnswerKind = "deja.blame-line"

// The two rules, named in the output rather than implied by which field is set:
// replacing the text a commit deleted proves the session made that change,
// having written the line only proves it wrote it once before the commit.
const (
	lineRuleReplaced = "replaced"
	lineRuleWrote    = "wrote"
)

func buildLineAnswer(target search.BlameTarget, c lineCommit, a lineAuthor, found bool, why string) lineAnswerJSON {
	out := lineAnswerJSON{
		Kind:          lineAnswerKind,
		SchemaVersion: jsonout.Version,
		File:          search.SafeLine(target.Base),
		Line:          target.Line,
		Why:           why,
	}
	if c.SHA != "" {
		commit := &lineCommitJSON{
			SHA: c.SHA,
			// A commit subject and author name are free text from a repository
			// deja did not write, the same as a transcript's.
			Subject: search.SafeLine(firstLine(c.Subject)),
			Author:  search.SafeLine(c.Author),
		}
		if !c.When.IsZero() {
			commit.When = c.When.UTC().Format(time.RFC3339)
		}
		out.Commit = commit
	}
	if !found {
		return out
	}
	out.Attributed = true
	out.Rule = lineRuleReplaced
	if a.Wrote {
		out.Rule = lineRuleWrote
	}
	out.Matched = search.SafeLine(a.Matched)
	out.SaidBefore = search.SafeLine(a.Said)
	out.Session = &lineSessionJSON{
		Harness: search.SafeLine(a.Session.Harness),
		ID:      a.Session.ID,
		Project: search.SafeLine(a.Session.Project),
		Asked:   search.SafeLine(a.Asked),
		Ctx:     "deja ctx " + shortID(a.Session.ID),
	}
	return out
}

func writeLineAnswerJSON(w io.Writer, answer lineAnswerJSON) error {
	return json.NewEncoder(w).Encode(answer)
}

// blameLineOnly serves the two line-level flags: the answer about the line, in
// prose or as the object, and the opt-in note on the commit that carried it.
func blameLineOnly(w io.Writer, dir string, target search.BlameTarget, hits []search.BlameHit, mode blameMode) error {
	c, author, found, why := lineAttribution(dir, target, hits)
	if mode.Attribution {
		if mode.JSON {
			if err := writeLineAnswerJSON(w, buildLineAnswer(target, c, author, found, why)); err != nil {
				return err
			}
		} else if why != "" {
			fmt.Fprintf(w, "%s:%d — %s\n", target.Base, target.Line, why)
		} else {
			printLineAuthor(w, target, c, author, found)
		}
	}
	if mode.GitNote {
		if why != "" {
			return fmt.Errorf("blame --git-note: %s", why)
		}
		return writeGitNote(w, target, c, author, found)
	}
	return nil
}

// gitStderrLine is the first line git wrote to stderr before it failed.
func gitStderrLine(err error) string {
	var ex *exec.ExitError
	if !errors.As(err, &ex) {
		return ""
	}
	for _, line := range strings.Split(string(ex.Stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// gitNoteRef is where an opt-in attribution is written: a ref of deja's own, so
// `git log --notes=deja` shows it and nothing deja writes lands in the notes
// ref git shows by default.
const gitNoteRef = "deja"

// gitNoteBody is the note as a teammate reads it in `git log --notes=deja`.
//
// It says which rule answered, because a note is a public claim and the two
// rules are not equally strong, and it ends with the command that opens the
// session — the note is a pointer to evidence, not a substitute for it.
func gitNoteBody(target search.BlameTarget, a lineAuthor) string {
	rule := lineRuleReplaced
	if a.Wrote {
		rule = lineRuleWrote
	}
	var b strings.Builder
	fmt.Fprintf(&b, "deja: %s:%d written in %s · %s · %s (rule: %s)\n",
		target.Base, target.Line, search.SafeLine(a.Session.Harness), shortID(a.Session.ID),
		search.SafeLine(a.Session.Project), rule)
	fmt.Fprintf(&b, "why, in full: deja ctx %s\n", shortID(a.Session.ID))
	return b.String()
}

// writeGitNote records the attribution on the commit git named.
//
// Opt-in, and only where there is something to record: a note is visible to
// everyone who fetches the ref, so a guess would be a published guess. The
// write is idempotent — a note already holding this body is left alone rather
// than appended to, since running blame twice on the same line is the normal
// way to read it.
func writeGitNote(w io.Writer, target search.BlameTarget, c lineCommit, a lineAuthor, found bool) error {
	if c.SHA == "" {
		return fmt.Errorf("blame --git-note: no commit to write a note on")
	}
	if !found {
		return fmt.Errorf("blame --git-note: nothing is attributed to a session, so there is no attribution to record")
	}
	dir := filepath.Dir(target.FullPath)
	body := gitNoteBody(target, a)
	if existing, err := gitRun(dir, "notes", "--ref="+gitNoteRef, "show", c.SHA); err == nil {
		if strings.Contains(existing, strings.TrimSpace(body)) {
			fmt.Fprintf(w, "refs/notes/%s already records this line on %s\n", gitNoteRef, shortSHA(c.SHA))
			return nil
		}
	}
	if _, err := gitRun(dir, "notes", "--ref="+gitNoteRef, "append", "-m", body, c.SHA); err != nil {
		// git's own first line, because "exit status 128" names nothing. The one
		// this hits in practice is a machine with no committer identity
		// configured — a note is a git object and needs one — which a reader can
		// fix and a bare status code cannot tell them about.
		if why := gitStderrLine(err); why != "" {
			return fmt.Errorf("blame --git-note: git could not write the note: %s", search.SafeLine(why))
		}
		return fmt.Errorf("blame --git-note: git could not write the note: %w", err)
	}
	fmt.Fprintf(w, "wrote refs/notes/%s on %s — `git log --notes=%s` shows it\n", gitNoteRef, shortSHA(c.SHA), gitNoteRef)
	return nil
}
