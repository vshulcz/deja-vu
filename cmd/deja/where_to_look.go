package main

import (
	"os"
)

// whereToLookLine names the documentation and the issue tracker. Most installs
// now arrive through a marketplace — `claude plugin install`, `npx skills add`,
// the dsh store — so the person never opens the README, and the only lines deja
// ever said to them were the install tail and the first-build notice. Neither
// said where to read more or where to report something (#3030).
//
// Once per machine: it is a pointer, not news, and a line that repeats is a
// line that gets skipped. The marker sits beside the index for the same reason
// the built note's does — one machine, one index directory.
const whereToLookLine = "docs: vshulcz.github.io/deja-vu · issues: github.com/vshulcz/deja-vu/issues"

// whereToLook returns the line the first time it is asked for on this machine,
// and "" afterwards. A store that has not been built yet is not the moment:
// the line belongs under a proof that just showed real sessions.
func whereToLook(dir string) string {
	if dir == "" {
		return ""
	}
	marker := dir + ".wheretolook"
	if _, err := os.Stat(marker); err == nil {
		return ""
	}
	if err := os.WriteFile(marker, []byte("1"), 0o600); err != nil {
		// Unwritable index directory: say it once here rather than every run.
		return ""
	}
	return whereToLookLine
}
