package main

import (
	"os"
	"path/filepath"
)

// dejaHookCommandMissing answers the loud half of this question: the wiring
// names a deja that is gone. The quiet half is a wiring that names a deja
// which is *there* and is not this one — an old install, a `go build` in /tmp,
// a probe binary in a scratch directory. It works until that file goes, and
// `doctor` said `wired` either way.
//
// Measured on the machine this was written on: `grok mcp list` printed
// `deja: ~/.claude/jobs/<id>/tmp/deja-prog mcp`, a build left behind by a probe
// run, while the row read `wired` — because the row is about the config file,
// and the config file was fine (#3656).
//
// What every real case had in common is not "a different binary" — that is
// ordinary, and on windows it is what every config looks like — but a binary in
// a directory something else will delete.
func dejaWiredElsewhere(path string) string {
	other := dejaHookCommandIn(path)
	if other == "" {
		// An MCP config keeps the binary in a `command` field and the
		// subcommand in `args`, so the hook reader — which wants a subcommand
		// after the path — sees nothing. That is how Hermes' entry, pointing
		// at a probe build in a scratch directory, stayed unreported after the
		// first version of this check (#3656).
		other = dejaCommandIn(path)
	}
	if other == "" || !filepath.IsAbs(other) {
		return ""
	}
	if _, err := os.Stat(other); err != nil {
		// Gone is the other check's business, and it says more.
		return ""
	}
	if !wiringPathIsTemporary(other) {
		return ""
	}
	return other
}

// wiringPathIsTemporary is the judgement, as a variable for the reason
// exeIsTemporary is one: a test's fixtures all live in a temp directory, so
// under a test binary this would answer yes to everything and the check could
// never be exercised either way.
var wiringPathIsTemporary = func(p string) bool {
	if underTestBinary() {
		return false
	}
	return underTempDir(p)
}

// dejaHookCommandIn is dejaHookCommandMissing's sibling: the deja binary a
// wiring file runs, whether or not it exists.
func dejaHookCommandIn(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(b)
	for _, loc := range hookExePath.FindAllStringIndex(text, -1) {
		if !hookExeBoundary(text, loc[0]-1) || !hookExeBoundary(text, loc[1]) {
			continue
		}
		if !hookExeRuns.MatchString(text[loc[1]:]) && !hookExeAssigned.MatchString(text[:loc[0]]) {
			continue
		}
		cand := quotedPathUnescape.Replace(text[loc[0]:loc[1]])
		if !filepath.IsAbs(cand) {
			continue
		}
		return cand
	}
	return ""
}

// samePathTarget compares two paths by what they resolve to, so a symlinked
// install — /usr/local/bin/deja pointing at the Cellar copy — is not reported
// as a different binary.
func samePathTarget(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// otherBinaryNote is the line doctor prints for it.
func otherBinaryNote(path, target string) string {
	other := dejaWiredElsewhere(path)
	if other == "" {
		return ""
	}
	return "runs " + other + ", a build in a temporary directory — it works until that directory is " +
		"cleaned; `deja install " + target + "` points the entry at this binary"
}
