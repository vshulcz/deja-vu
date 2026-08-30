package main

import (
	"os"
	"path/filepath"
	"strings"
)

// dejaHookCommandMissing returns the deja binary a hook file runs when that
// binary is not there, and "" otherwise. The MCP check reads a server entry,
// where the binary is a field of its own; a hook is a command line, so the
// binary is its first word and the subcommand follows.
//
// Same reasoning as dejaCommandMissing for what is skipped: a bare name is the
// PATH's business, and a relative path resolves against wherever the reader is
// standing, so neither can be answered by a stat here. A binary under a
// directory with a space in its name is skipped for the same reason — the
// first word is not the whole path, and saying nothing beats calling a working
// install broken.
func dejaHookCommandMissing(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, m := range commandValue.FindAllStringSubmatch(string(b), -1) {
		var value string
		switch {
		case m[1] != "":
			value = quotedPathUnescape.Replace(m[1])
		case m[2] != "":
			value = m[2]
		default:
			value = m[3]
		}
		bin, _, _ := strings.Cut(strings.TrimSpace(value), " ")
		if !commandIsDeja(bin) || !filepath.IsAbs(bin) {
			continue
		}
		if _, err := os.Stat(bin); err != nil {
			return bin
		}
	}
	return ""
}

// hookExeNote is the line a row prints under itself when the hook it just
// called wired runs a binary that is gone. install target is the -auto one:
// the plain target writes the MCP entry, and following advice that repairs a
// different file leaves the hook dead and doctor quiet about it (#2686).
func hookExeNote(path, target string) string {
	missing := dejaHookCommandMissing(path)
	if missing == "" {
		return ""
	}
	return "runs " + missing + ", which is not there — `deja install " + target + "` rewrites it for this binary"
}
