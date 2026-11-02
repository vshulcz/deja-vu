package query

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// ProjectMatches reports whether a --project filter selects this session.
//
// The stored project of an imported session carries an "imported:" prefix, and
// `deja last` renders it with the sending machine's name in place of that
// prefix — which is the answer to "where did this come from" once three
// machines are exchanging history. That rendering was the one label no filter
// accepted: `--project Host:alpha` matched nothing while `--project
// imported:alpha` matched, so the string a reader can copy off the screen
// worked nowhere (#2644).
//
// Both forms match here. The stored one keeps its substring rule, which is what
// makes `--project alpha` and `--project imported` both work; the rendered one
// is matched the same way once the machine name is put back.
func ProjectMatches(project, from, want string) bool {
	if want == "" {
		return true
	}
	if strings.Contains(strings.ToLower(project), strings.ToLower(want)) {
		return true
	}
	// Only for a want that carries the separator. Without that guard,
	// `--project mini` would select every session from a machine called mini
	// rather than a project of that name — a wider answer than the flag has
	// ever given, and `--from` is the flag for a machine.
	if !strings.Contains(want, ":") {
		return false
	}
	shown := DisplayProject(project, from)
	return shown != project && strings.Contains(strings.ToLower(shown), strings.ToLower(want))
}

// ProjectAnyMatches is ProjectMatches over a set of names. A project has more
// than one name form — a worktree of a repository records its own — so a
// surface scoped to "the project I was asked in" has to ask about all of them
// or it misses its own worktrees (#3713). No names means no filter, the way an
// empty --project has always meant the machine.
func ProjectAnyMatches(project, from string, wants []string) bool {
	if len(wants) == 0 {
		return true
	}
	for _, want := range wants {
		if want = strings.TrimSpace(want); want == "" {
			continue
		}
		if ProjectMatches(project, from, want) {
			return true
		}
	}
	return false
}

// DisplayProject is the label a reader is shown for a session's project: the
// machine it came from in place of the "imported:" prefix, and the stored
// project otherwise. It lives here so the filter and the screen cannot drift
// apart.
func DisplayProject(project, from string) string {
	if from == "" {
		return project
	}
	rest, ok := strings.CutPrefix(project, model.ImportedPrefix)
	if !ok {
		return project
	}
	return from + ":" + rest
}

// ProjectInScope reports whether a session's project is the one a caller is
// standing in. Exported so the automatic surfaces share one rule: the same
// question answered different ways is how a client's project reached a
// session start (#2333), a handoff (#2336) and a tool-time line (#2339).
func ProjectInScope(project, want string) bool {
	if want == "" {
		return false
	}
	return LowerProjectInScope(strings.ToLower(project), strings.ToLower(want))
}

// LowerProjectInScope is the rule of ProjectInScope on names already lowered.
func LowerProjectInScope(p, w string) bool {
	if w == "" {
		return false
	}
	imported := false
	if rest, ok := strings.CutPrefix(p, model.ImportedPrefix); ok {
		// A synced session carries the peer's prefix and is otherwise the same
		// project under the same name.
		p, imported = rest, true
	}
	w = strings.TrimPrefix(w, model.ImportedPrefix)
	if p == w {
		return true
	}
	// A candidate with no separator is a bare directory name, and as a suffix
	// against a LOCAL project it cannot tell two directories apart: working in
	// /work/api, the candidate "api" matched a client's acme/api and the
	// session-start hook injected it "from this project" (#2333).
	//
	// A peer's project keeps the peer's path, which is not this machine's, so
	// the loose rule stays for imported work: matching it by the name this
	// machine knows it by is the point of scoping a synced index.
	if !imported && !strings.ContainsAny(w, `/\`) {
		return false
	}
	return endsWithSegment(p, w)
}

// ProjectInScopeStrict is ProjectInScope without the allowance synced work
// gets. The loose rule is right for recall and wrong for a command that
// packages one session's content for another agent (#2347).
func ProjectInScopeStrict(project, want string) bool {
	if want == "" {
		return false
	}
	p := strings.TrimPrefix(strings.ToLower(project), model.ImportedPrefix)
	w := strings.TrimPrefix(strings.ToLower(want), model.ImportedPrefix)
	if p == w {
		return true
	}
	if !strings.ContainsAny(w, `/\`) {
		return false
	}
	return endsWithSegment(p, w)
}

// EndsWithSegment is strings.HasSuffix(p, "/"+w) || strings.HasSuffix(p, `\`+w)
// without building either string.
func EndsWithSegment(p, w string) bool {
	return endsWithSegment(p, w)
}

func endsWithSegment(p, w string) bool {
	n := len(p) - len(w) - 1
	return n >= 0 && (p[n] == '/' || p[n] == '\\') && p[n+1:] == w
}
