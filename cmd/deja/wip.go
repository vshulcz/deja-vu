package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// `deja wip` answers the question a session asks after it loses its context:
// what was I doing.
//
// Not `resume`: that name is taken by the command that reopens a session in its
// own harness, which is a different job.
//
// Measured on this machine's transcripts, a cold session takes a median of 10
// actions before its first edit and a session after a compaction takes 28, p75
// 61, across 86 compactions — the largest single cost measurable in the tool.
// Nothing is missing from the store when that happens. What is missing is the
// shape, and the shape is derived: what was asked, what the session settled,
// which files are in flight, what was last run. Counted over those 96
// compactions, the material is there 94% of the time.
//
// Derived on purpose. deja already has two places an agent can write and both
// are nearly empty — 9 writes against 3793 injections over sixteen days — so
// this asks nothing of the agent.
func runWIP(dir string, args []string, stdout io.Writer) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("wip takes --json and nothing else")
		}
	}
	s, r, ok, err := wipSession(dir)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(wipJSON{
			SchemaVersion: jsonout.Version,
			Session:       s.ID,
			Harness:       s.Harness,
			Asked:         r.Asked,
			Decision:      r.Decision,
			Files:         r.Files,
			Command:       r.Command,
			Failed:        r.Failed,
			Lines:         r.Lines(),
		})
	}
	if !ok {
		fmt.Fprintln(stdout, "deja: no session in this project to pick up")
		return nil
	}
	lines := r.Lines()
	if len(lines) == 0 {
		fmt.Fprintln(stdout, "deja: the last session in this project left nothing to pick up")
		return nil
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, safeWIPLine(l))
	}
	return nil
}

// wipJSON is the shape a caller reads. Every field is omitted rather than
// zero-valued where absent, the way the fix and friction envelopes are: a
// missing decision is a real state and "" is not one.
type wipJSON struct {
	SchemaVersion int      `json:"schema_version"`
	Session       string   `json:"session,omitempty"`
	Harness       string   `json:"harness,omitempty"`
	Asked         string   `json:"asked,omitempty"`
	Decision      string   `json:"decision,omitempty"`
	Files         []string `json:"files,omitempty"`
	Command       string   `json:"command,omitempty"`
	Failed        bool     `json:"command_failed,omitempty"`
	Lines         []string `json:"lines,omitempty"`
}

// resumeSession is the session to pick up: the newest one in this project that
// the trust policy allows, read in full because the handover is about its last
// few turns. The project rather than the machine — somebody else's work is not
// a handover.
func wipSession(dir string) (model.Session, digest.Resume, bool, error) {
	// The same lookup the session-start hook uses: the project by every name it
	// answers to, plus the working directory itself, because a session started
	// in a subdirectory keeps that directory's name and no list of names reaches
	// down to it (#2040). Asking by name alone missed this very session, whose
	// stored project is the tail of a worktree path.
	cwd := hookCWD("")
	ss, err := index.RecentProjectsUnder(dir, digest.ProjectNameCandidates(cwd), cwd, wipScan)
	if err != nil {
		return model.Session{}, digest.Resume{}, false, err
	}
	pol := policy.Load()
	var fallback model.Session
	var fallbackResume digest.Resume
	for _, s := range ss {
		if len(s.Messages) == 0 || !pol.Allows(policy.ActivationSearch, s.Project) {
			continue
		}
		r := digest.ResumeFrom(s, index.LooksLikeError)
		// The newest session is not always the one worth picking up: a probe, a
		// one-line question or an aborted run is newer than the work. Two facts
		// out of the four is the bar for calling something a handover; anything
		// non-empty is kept as the answer of last resort, because "what were we
		// doing" deserves something over nothing.
		if wipFacts(r) >= 2 {
			return s, r, true, nil
		}
		if fallback.ID == "" && !r.Empty() {
			fallback, fallbackResume = s, r
		}
	}
	if fallback.ID != "" {
		return fallback, fallbackResume, true, nil
	}
	return model.Session{}, digest.Resume{}, false, nil
}

// resumeFacts counts how much of the handover a session actually yielded.
func wipFacts(r digest.Resume) int {
	n := 0
	for _, has := range []bool{r.Asked != "", r.Decision != "", r.Command != "", len(r.Files) > 0} {
		if has {
			n++
		}
	}
	return n
}

// wipScan is how many recent sessions to look through for one in this
// project. A handful: the answer is the newest, and reading further only costs
// the reader time.
const wipScan = 25

// safeWIPLine is what reaches a terminal: the text came out of a transcript,
// so an escape byte in it would reach the screen.
func safeWIPLine(l string) string { return search.SafeLine(l) }
