package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// earlierNameJointCap bounds how many files the one session that saw both may
// have touched. A rename lands in a session that is about the rename; a
// thirteen-file refactor touches everything, and naming whichever of them
// happened to stop first is a coin toss dressed as a finding.
const earlierNameJointCap = 3

// earlierNameNote points at a file this one's history may continue from.
//
// blame answers under the name you type, so the moment a file is renamed
// everything discussed under the old name drops out — and the name a reader
// has is the one in their editor (#1627). Following the rename is the other
// option and it is guesswork: "rename X to Y" in a plan nobody carried out
// reads exactly like one that was done, and a wrong link silently attributes
// one file's history to another.
//
// So this asserts nothing. It reports what the index saw — one session touched
// both files, the other one stopped there, this one went on — and leaves the
// reader to say whether that was a rename. The conditions are narrow for the
// same reason: two files edited together once are not evidence of anything, so
// the pair has to be nearly alone in its session, in the same directory, in a
// project this file was worked on in, and the older file's last touch has to
// be that very session.
func earlierNameNote(dir, typed string, target search.BlameTarget) string {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	lastTouch := map[string]time.Time{}
	type joint struct {
		when    time.Time
		project string
		session string
	}
	together := map[string]joint{}
	var targetFirst, targetLast time.Time
	targetProjects := map[string]bool{}
	// The directories the file itself was recorded under, so a candidate can
	// be held to the same one without guessing how the reader's typed path
	// maps onto the absolute path a session wrote.
	targetDirs := map[string]bool{}
	for _, m := range metas {
		when := m.Updated
		if when.IsZero() {
			when = m.Started
		}
		touchedTarget := false
		for _, p := range m.Touched {
			if blameSamePath(p, typed, target) {
				touchedTarget = true
				break
			}
		}
		for _, p := range m.Touched {
			if blameSamePath(p, typed, target) {
				if targetFirst.IsZero() || when.Before(targetFirst) {
					targetFirst = when
				}
				if when.After(targetLast) {
					targetLast = when
				}
				targetProjects[m.Project] = true
				targetDirs[path.Dir(filepath.ToSlash(p))] = true
				continue
			}
			if when.After(lastTouch[p]) {
				lastTouch[p] = when
			}
			// The session that saw both, and only when it is small enough to
			// be about these two files.
			if touchedTarget && len(m.Touched) <= earlierNameJointCap {
				if prev, ok := together[p]; !ok || when.After(prev.when) {
					together[p] = joint{when: when, project: m.Project, session: m.ID}
				}
			}
		}
	}
	if targetFirst.IsZero() {
		return ""
	}
	best, bestWhen := "", time.Time{}
	for p, j := range together {
		// It stopped where this one carried on: the joint session is the last
		// time anything touched it, and this file outlived it.
		if !lastTouch[p].Equal(j.when) || !j.when.Before(targetLast) {
			continue
		}
		if !targetProjects[j.project] {
			continue
		}
		// Same directory and same kind of file. A rename moves a name, not a
		// file's place in the tree — and when it does move one, saying nothing
		// beats pointing at a vendored copy or a file from another package.
		if !targetDirs[path.Dir(filepath.ToSlash(p))] {
			continue
		}
		if path.Ext(p) != path.Ext(target.Base) {
			continue
		}
		// The timing is the same for a rename and for two files that merely
		// met once — a test beside its subject, a caller and the file it
		// replaced. So the session that saw both has to say it moved one name
		// to the other. Prose alone would be guesswork, which is why #1627
		// rejected it; prose as the second signal is what separates the two
		// shapes the first one cannot.
		if !sessionSaysItMoved(dir, j.session, path.Base(p), target.Base) {
			continue
		}
		if best == "" || j.when.After(bestWhen) {
			best, bestWhen = p, j.when
		}
	}
	if best == "" {
		return ""
	}
	// What was seen, not what it means: deja cannot tell a rename from a file
	// that was deleted while this one took over its work, and calling it a
	// rename would be the wrong link this exists to avoid.
	return fmt.Sprintf("deja: %s was last touched in the same session as this file — `deja blame %s` if that is where its history continues from\n",
		search.SafeLine(shortHome(best)), pasteSafe(best))
}

// blameSamePath asks whether a touched path is the file blame was asked about.
// What the reader typed, not what deja resolved it to: the file is named the
// way their editor shows it — `internal/search/x.go` — while the session
// recorded whatever absolute path that machine had.
//
// The basename alone is not enough for a typed path: `internal/a/util.go` and
// `internal/b/util.go` are two files, and folding them together dragged one
// file's first touch back to the other's, which killed the very renames this
// note is about.
func blameSamePath(touched, typed string, target search.BlameTarget) bool {
	if touched == target.FullPath {
		return true
	}
	q := strings.TrimPrefix(filepath.ToSlash(typed), "./")
	t := filepath.ToSlash(touched)
	if q == "" {
		return false
	}
	if t == q || strings.HasSuffix(t, "/"+q) {
		return true
	}
	// A reader who typed a bare name means the file of that name, and there is
	// nothing else to go on — but only when they typed a bare name.
	return !strings.Contains(q, "/") && path.Base(t) == q
}

// sessionSaysItMoved reads the one session that touched both files and asks
// whether it names them together in a sentence about moving a name — `git mv`,
// "rename x to y". One session, read only when the touch data has already
// narrowed the field to a single candidate.
func sessionSaysItMoved(dir, id, from, to string) bool {
	s, ok, err := index.FindByPrefix(dir, id)
	if err != nil || !ok {
		return false
	}
	for _, m := range s.Messages {
		text := strings.ToLower(m.Text)
		if !strings.Contains(text, strings.ToLower(from)) || !strings.Contains(text, strings.ToLower(to)) {
			continue
		}
		for _, verb := range []string{"rename", "renaming", "mv ", "moved", "move "} {
			if strings.Contains(text, verb) {
				return true
			}
		}
	}
	return false
}
