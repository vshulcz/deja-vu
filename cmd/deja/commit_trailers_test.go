package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// No commit in this repository carries an AI co-author or a session link. The
// rule is written down in .claude/rules/commit.md and was still broken six
// times over three days by a harness that appends its own trailer, and then
// the history had to be rewritten and force-pushed. A test fails before the
// push instead.
//
// Recent history only, because CI checks out a shallow clone; what it
// measures is the commits a PR would land, not the archive.
func TestNoCommitCarriesAnAITrailer(t *testing.T) {
	out, err := exec.Command("git", "log", "-60", "--format=%H%x00%B%x01").Output()
	if err != nil {
		t.Skipf("git log unavailable here: %v", err)
	}
	trailer := regexp.MustCompile(`(?im)^\s*(co-authored-by:.*anthropic\.com|claude-session:|.*claude\.ai/code/session_|.*generated with \[?claude code)`)
	for _, rec := range strings.Split(string(out), "\x01") {
		hash, body, ok := strings.Cut(rec, "\x00")
		if !ok {
			continue
		}
		if m := trailer.FindString(body); m != "" {
			t.Errorf("commit %s carries %q — strip it (commit.md: no Co-Authored-By, ever)", strings.TrimSpace(hash)[:12], strings.TrimSpace(m))
		}
	}
}
