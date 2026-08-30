package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hookExePath matches an absolute path to deja's own binary. Both forms are
// written by deja itself: the unix one, and the windows one a windows build
// reads back. The case fold covers the drive letter and the suffix only — on a
// case-sensitive filesystem /opt/Deja is a different file, and calling it the
// binary reports a healthy install broken.
var hookExePath = regexp.MustCompile("(/[^\\s\"'`\\\\]*/deja(?:\\.[eE][xX][eE])?|[A-Za-z]:[\\\\/][^\\s\"'`\\n]*[\\\\/]deja(?:\\.[eE][xX][eE])?)")

// hookExeRuns matches what follows the path when the path is being run: one of
// deja's own subcommands, past whatever quoting the file uses.
var hookExeRuns = regexp.MustCompile(`^["'\\\s]*(hook-[a-z-]+|warmup-status|recall|statusline|mcp)\b`)

// hookExeAssigned matches what precedes the path when a generated plugin binds
// it to a name of its own — `const DEJA = "…"`, `DEJA = "…"`. The `=` is what
// keeps a config *key* that ends in deja out: `"DEJA_INDEX_DIR": "…/deja"` is
// a directory this check has no business reporting on.
var hookExeAssigned = regexp.MustCompile(`(?i)deja[a-z_]*["']?\s*=\s*["'\\]*$`)

// dejaHookCommandMissing returns the deja binary a hook file runs when that
// binary is not there, and "" otherwise.
//
// The MCP check reads a server entry, where the binary is a field of its own.
// A hook is whatever the harness accepts: a command line, a `const DEJA = …`
// in a plugin deja generates, a path quoted inside a command. So this reads
// the path itself rather than the syntax around it — but only where the file
// is running it or binding it, because these files also carry text nobody
// executes. aider's is a digest of past sessions, and a transcript that once
// mentioned a checkout called deja would otherwise be read as a dead hook.
//
// Same reasoning as dejaCommandMissing for what is skipped: a bare name is the
// PATH's business, and a relative path resolves against wherever the reader is
// standing, so neither can be answered by a stat here. A path with a space in
// it is skipped too — the boundary that ends every other path ends that one in
// the middle, and saying nothing beats calling a working install broken.
func dejaHookCommandMissing(path string) string {
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
		// The windows form is written escaped, and the escapes are not part of
		// the path: stat would miss and the note would print them back.
		cand := quotedPathUnescape.Replace(text[loc[0]:loc[1]])
		if !filepath.IsAbs(cand) {
			continue
		}
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
	return ""
}

// hookExeBoundary reports whether the byte at i ends a path rather than
// continuing one. Either end of the file counts: a file holding the path and
// nothing else is one deja writes too. `:` is deliberately not a boundary — a
// permission rule reads `Bash(/home/me/bin/deja:*)`, and that is not a hook.
func hookExeBoundary(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return true
	}
	switch text[i] {
	case '"', '\'', '`', '\\', ' ', '\t', '\r', '\n', ',', ';', '=', '(', ')', '[', ']', '{', '}', '|', '&', '<', '>':
		return true
	}
	return false
}

// hookExeNote is the line a row prints under itself when the hook it just
// called wired runs a binary that is gone. The install target is the -auto
// one: the plain target writes the MCP entry, and following advice that
// repairs a different file leaves the hook dead and doctor quiet about it
// (#2686).
func hookExeNote(path, target string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	missing := dejaHookCommandMissing(path)
	if missing == "" {
		return ""
	}
	return "runs " + missing + ", which is not there — `deja install " + target + "` rewrites it for this binary"
}
