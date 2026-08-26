package search

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// scratchRoots are directories an agent runtime owns rather than a person: a
// background job's own temp tree. A session recorded there is the tool being
// driven, not work being done.
//
// Measured on a real store: 207 of the 300 most recent sessions sat under
// .claude/jobs/<id>/tmp with a median of 42 words, and one of them — a one-shot
// transcript of a question — outranked the session that had settled that
// question, because a repeated question matches word for word (#2050).
//
// The system temp directory is deliberately absent. It looked like the same
// thing and is not: every Go test's t.TempDir() lands under /var/folders on
// macOS, so including it hid four hook fixtures the moment it ran, and a person
// who starts an agent in a mktemp -d is doing real work in a real directory.
var scratchRoots = []string{
	"/.claude/jobs/",
}

// WithoutScratch drops the sessions an agent runtime recorded in its own
// scratch tree.
//
// Applied where sessions enter a search rather than inside the scorer: the
// error-signature and relevance tiers build their hits without going through
// it, so a filter there covered one path of three and the transcript still came
// back (#2050).
func WithoutScratch(ss []model.Session) []model.Session {
	out := ss[:0:0]
	for _, s := range ss {
		if isScratchSession(s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// isScratchSession reports whether a session was recorded inside a directory an
// agent runtime made for itself.
//
// Both fields are checked rather than chosen between: opencode keeps one
// database for every session, so its Path is the store and the Project carries
// the working directory, while the file-per-session harnesses put the directory
// in the Path.
func isScratchSession(s model.Session) bool {
	hay := s.Path + " " + s.Project
	for _, root := range scratchRoots {
		if strings.Contains(hay, root) {
			return true
		}
	}
	return false
}
