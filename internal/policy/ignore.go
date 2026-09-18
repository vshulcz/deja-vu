package policy

import (
	"path"
	"strings"
)

// defaultIgnore is the one directory deja never recalls from without being
// asked: the temp tree a background agent's own runtime makes for itself.
//
// Measured on a real store, 207 of the 300 most recent sessions sat under
// .claude/jobs/<id>/tmp with a median of 42 words, and one of them — a one-shot
// transcript of a question — outranked the session that had settled that
// question, because a repeated question matches word for word (#2050).
//
// The system temp directory is deliberately not here. Every Go test's
// t.TempDir() lands under /var/folders on macOS, and a person who starts an
// agent in a mktemp -d is doing real work in a real directory; anyone who wants
// it excluded can say so in the file.
var defaultIgnore = []string{"*/.claude/jobs/*"}

// Ignored reports whether a session recorded at this path, in this project,
// should stay out of recall.
//
// Patterns are shell globs matched against the path and against the project,
// because the harnesses disagree about which one carries the working directory:
// opencode keeps a single database, so its path is the store and the project is
// the directory, while the file-per-session harnesses put the directory in the
// path.
//
// A pattern in the file replaces the default rather than adding to it. Someone
// who writes the key has an opinion about what deja should skip, and silently
// keeping a rule they did not write is the kind of surprise that makes a config
// file untrustworthy — `deja doctor` prints what is in force.
func (p Policy) Ignored(sessionPath, project string) bool {
	pats := p.Ignore
	if len(pats) == 0 {
		pats = defaultIgnore
	}
	for _, pat := range pats {
		if matchesAnywhere(pat, sessionPath) || matchesAnywhere(pat, project) {
			return true
		}
	}
	return false
}

// matchesAnywhere is path.Match with the leniency a person expects from a
// directory rule: a bare fragment matches anywhere in the string, so
// "*/.claude/jobs/*" catches the tree wherever the home directory sits.
func matchesAnywhere(pattern, s string) bool {
	if s == "" {
		return false
	}
	// Both sides in the one spelling. A rule is a directory rule and is
	// written with forward slashes; a session path on Windows carries
	// backslashes, and compared as they are the rule matched nothing there —
	// so a tree deja was told not to recall was recalled on every surface
	// (#2808). Normalising the rule too means someone who writes it the
	// Windows way is understood as well.
	pattern = strings.ReplaceAll(pattern, `\`, "/")
	s = strings.ReplaceAll(s, `\`, "/")
	if ok, err := path.Match(pattern, s); err == nil && ok {
		return true
	}
	// A glob only matches the whole string, and a session path is longer than
	// the rule that describes it. Fall back to the literal middle of the
	// pattern, which is what a directory rule means.
	if lit := strings.Trim(pattern, "*"); lit != "" && !strings.ContainsAny(lit, "*?[") {
		if strings.Contains(s, lit) {
			return true
		}
		return strings.Contains(decodeCWDSegments(s), lit)
	}
	return false
}

// decodeCWDSegments spells out the directories a harness folded into one name.
//
// omp, claude and the stores shaped like them record a session under a
// directory whose name is the working directory with its separators replaced by
// dashes — `-.claude-jobs-<id>-tmp-dsh-work`. A directory rule written the way
// a person writes it cannot match that, so the tree deja is told to keep out of
// recall stayed in: measured on a real store, 12 of the 317 rows naming a job
// tree were served, all of them from a harness that encodes this way (#3746).
//
// Only a segment that begins with a dash is decoded, which is what these
// encoders produce because the path they encode begins with a separator. A file
// or directory that merely has dashes in it — `my-jobs-list.jsonl` — is left
// alone, so a rule cannot reach further than the person who wrote it meant.
func decodeCWDSegments(s string) string {
	if !strings.Contains(s, "/-") && !strings.HasPrefix(s, "-") {
		return s
	}
	parts := strings.Split(s, "/")
	for i, seg := range parts {
		if i == len(parts)-1 || !strings.HasPrefix(seg, "-") {
			continue
		}
		// A dot-prefixed directory arrives as `--`, since the encoder replaced
		// both the separator and the dot: `/.claude` is `--claude`.
		seg = strings.ReplaceAll(seg, "--", "/.")
		parts[i] = strings.ReplaceAll(seg, "-", "/")
	}
	return strings.Join(parts, "/")
}

// IgnorePatterns is what is in force, for doctor to print.
func (p Policy) IgnorePatterns() []string {
	if len(p.Ignore) == 0 {
		return defaultIgnore
	}
	return p.Ignore
}
