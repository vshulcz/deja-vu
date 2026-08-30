package query

import "strings"

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

// DisplayProject is the label a reader is shown for a session's project: the
// machine it came from in place of the "imported:" prefix, and the stored
// project otherwise. It lives here so the filter and the screen cannot drift
// apart.
func DisplayProject(project, from string) string {
	if from == "" {
		return project
	}
	rest, ok := strings.CutPrefix(project, "imported:")
	if !ok {
		return project
	}
	return from + ":" + rest
}
