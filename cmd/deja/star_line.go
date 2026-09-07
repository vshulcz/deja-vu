package main

import "os"

// starText is the one sentence deja says about itself, once per index: after
// the install proof, or in the first week note if the install never printed
// it. deja has no telemetry and no account, so a star is the only signal that
// reaches the project from a machine that uses it — and the install proof is
// the moment the reader has just been shown something real. Once, because the
// second time it is a nag, and a nag costs more than a star is worth.
const starText = "if deja earns its keep, a star on GitHub helps the next person find it — github.com/vshulcz/deja-vu"

// starLine returns the sentence the first time it is asked for a given index
// and "" after that. The marker sits beside the index like .builtnote and
// .weeknote, so a rebuild does not repeat it and a second machine gets its own.
func starLine(dir string) string {
	p := dir + ".starnote"
	if _, err := os.Stat(p); err == nil {
		return ""
	}
	if err := os.WriteFile(p, []byte("1\n"), 0o600); err != nil {
		return ""
	}
	return starText
}
