package main

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// displayProject is the project label a reader sees. A session that arrived by
// sync carries its project under an "imported:" prefix, which says the work
// happened somewhere else and stops there. With three machines exchanging
// history that is the whole answer to "where did this come from" — so where
// the sending machine named itself, its name goes in place of the prefix.
//
// The stored project is untouched: "imported:" is what the trust policy reads,
// what resume checks before offering a command for another machine's session,
// and what the note lifecycle keys on. This is the rendering, not the record.
func displayProject(s model.Session) string {
	if s.From == "" {
		return s.Project
	}
	rest, ok := strings.CutPrefix(s.Project, "imported:")
	if !ok {
		return s.Project
	}
	return s.From + ":" + rest
}
