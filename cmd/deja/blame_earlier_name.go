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

// earlierNameNote points at the file this one's history stops at.
//
// blame answers under the name you type, so the moment a file is renamed
// everything discussed under the old name drops out — and the name a reader
// has is the one in their editor (#1627). Following the rename is the other
// option and it is guesswork: "rename X to Y" in a plan nobody carried out
// reads exactly like one that was done, and a wrong link silently attributes
// one file's history to another. So deja names the candidate and lets the
// reader decide.
//
// The signal is touch data rather than prose: one session touched both files,
// the other one was never touched after it, and this one was. That is a rename
// as far as the index can see it, without reading anybody's intent.
func earlierNameNote(dir, typed string, target search.BlameTarget) string {
	metas, err := index.AllMeta(dir)
	if err != nil {
		return ""
	}
	type span struct{ first, last time.Time }
	seen := map[string]*span{}
	together := map[string]bool{}
	var targetSpan span
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
				if targetSpan.first.IsZero() || when.Before(targetSpan.first) {
					targetSpan.first = when
				}
				if when.After(targetSpan.last) {
					targetSpan.last = when
				}
				continue
			}
			s := seen[p]
			if s == nil {
				s = &span{first: when}
				seen[p] = s
			}
			if when.Before(s.first) {
				s.first = when
			}
			if when.After(s.last) {
				s.last = when
			}
			if touchedTarget {
				together[p] = true
			}
		}
	}
	if targetSpan.first.IsZero() {
		return ""
	}
	best := ""
	var bestLast time.Time
	for p := range together {
		s := seen[p]
		// The shape of a rename: the other file was already being worked on
		// before this one existed, and nothing touched it after the session
		// that touched both.
		if !s.first.Before(targetSpan.first) || s.last.After(targetSpan.last) {
			continue
		}
		if path.Ext(p) != path.Ext(target.Base) || path.Base(p) == target.Base {
			continue
		}
		if best == "" || s.last.After(bestLast) {
			best, bestLast = p, s.last
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("deja: this file's history stops here — %s was worked on before it and not since; `deja blame %s` for what came before\n",
		search.SafeLine(shortHome(best)), pasteSafe(best))
}

// blameSamePath asks whether a touched path is the file blame was asked about,
// on the same terms the search does: the reader types what their editor shows,
// which is rarely the absolute path a session recorded.
func blameSamePath(touched, typed string, target search.BlameTarget) bool {
	if touched == target.FullPath {
		return true
	}
	// What the reader typed, not what deja resolved it to: the file is named
	// the way their editor shows it — `internal/search/x.go` — while the
	// session recorded whatever absolute path that machine had.
	q := strings.TrimPrefix(filepath.ToSlash(typed), "./")
	t := filepath.ToSlash(touched)
	if q != "" && (t == q || strings.HasSuffix(t, "/"+q)) {
		return true
	}
	return path.Base(t) == target.Base
}
