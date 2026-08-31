package main

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// earlierNameJointCap bounds how many files the one session that saw both may
// have touched. A rename lands in a session that is about the rename; a
// thirteen-file refactor touches everything, and naming whichever of them
// happened to stop first is a coin toss dressed as a finding.
const earlierNameJointCap = 3

// earlierNameReadCap bounds how many candidate sessions are read to confirm a
// move. The candidates are tried newest first, so the cap costs only the
// oldest of a crowded field — and a crowded field is the case where the answer
// was least worth trusting anyway.
const earlierNameReadCap = 3

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
	// Newest first, then by path: a map's order is not an order, and on equal
	// timestamps — one per session, so ties are ordinary — the same command
	// answered with a different file on each run.
	candidates := make([]string, 0, len(together))
	for p := range together {
		candidates = append(candidates, p)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := together[candidates[i]], together[candidates[j]]
		if !a.when.Equal(b.when) {
			return a.when.After(b.when)
		}
		return candidates[i] < candidates[j]
	})
	best := ""
	reads := 0
	for _, p := range candidates {
		j := together[p]
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
		//
		// Each of these reads a session, so the field is bounded: a hub file
		// with ten one-off neighbours cost ten reads and most of blame's own
		// wall-clock.
		if reads >= earlierNameReadCap {
			break
		}
		reads++
		if !sessionSaysItMoved(dir, j.session, path.Base(p), target.Base) {
			continue
		}
		best = p
		break
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
	from, to = strings.ToLower(from), strings.ToLower(to)
	for _, m := range s.Messages {
		// Not what a file says about itself: an edit's payload is carried in
		// the message text, so a file whose contents read "renamed from x" was
		// voting for a rename nobody performed.
		if m.Role == sources.RoleFiles || m.Role == sources.RoleEdit || m.Role == "tool_result" {
			continue
		}
		text := strings.ToLower(m.Text)
		if sessionTextSaysItMoved(text, from, to) {
			return true
		}
	}
	return false
}

// sessionTextSaysItMoved asks whether one message says these two names were
// the same file under two names. Both have to be there, and the verb has to
// sit beside one of them.
func sessionTextSaysItMoved(text, from, to string) bool {
	text, from, to = strings.ToLower(text), strings.ToLower(from), strings.ToLower(to)
	i, j := strings.Index(text, from), strings.Index(text, to)
	if i < 0 || j < 0 {
		return false
	}
	first, last := i, j+len(to)
	if j < i {
		first, last = j, i+len(from)
	}
	return movedBetween(text, first, last)
}

// movedBetween looks for the verb in the span that holds both names, rather
// than anywhere in the message: a turn that renames two other files and
// mentions ours in passing said nothing about ours (#1627).
func movedBetween(text string, from, to int) bool {
	// Ahead of the pair, or in a gap short enough to be one clause. A rename
	// is written before its names — "rename x to y", "git mv x y" — and
	// anything between two mentions far apart is somebody else's sentence:
	// "old.go still leaks; also rename helper.go to helpers.go; and new.go
	// needs the same guard" put an unrelated rename between ours.
	if hasMoveVerb(text[max(0, from-moveVerbReach):from]) {
		return true
	}
	if to-from <= moveVerbReach {
		return hasMoveVerb(text[from:min(len(text), to)])
	}
	return false
}

// hasMoveVerb reads the words that mean a name was moved. "remove" is not
// "move": it contains it, and matching the substring made the deletion case —
// the one thing this note cannot tell from a rename — vote for itself.
func hasMoveVerb(span string) bool {
	for _, verb := range []string{"rename", "renaming", "renamed", "git mv ", "mv "} {
		if strings.Contains(span, verb) {
			return true
		}
	}
	for _, verb := range []string{" move ", " moved ", " moving "} {
		if strings.Contains(" "+span+" ", verb) {
			return true
		}
	}
	return false
}

// moveVerbReach is how far either side of the two names the verb may sit. A
// rename sentence puts it next to them; a paragraph that happens to hold both
// puts it anywhere.
const moveVerbReach = 40
