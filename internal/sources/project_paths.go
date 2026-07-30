package sources

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A Claude session takes its project from the directory the transcript lives in,
// which is the directory the agent was started from. Start it from a home
// directory — as anyone running one agent across several repositories does — and
// every session on the machine lands in a single bucket named after that home.
//
// Measured on a real corpus: every Claude session carried one project name, so
// `--project` could not separate seven repositories, and the file relevance work
// in #542 collided across all of them.
//
// The paths a session touched say where the work actually was. Of the sessions
// with project-shaped paths, 8 of 9 have one dominant repository.

// projectFromPaths returns the repository most of a session's file activity
// happened in, or "" when there is no clear winner. A majority is required
// rather than a plurality on purpose: a session that genuinely spans two repos
// should keep its directory-derived name instead of being filed under whichever
// one it touched marginally more.
func projectFromPaths(ms []model.Message) string {
	counts := map[string]int{}
	total := 0
	for _, m := range ms {
		if m.Role != RoleFiles {
			continue
		}
		for _, p := range strings.Split(m.Text, "\n") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if agentScratch(p) {
				continue
			}
			root := repoRoot(filepath.Dir(p))
			if root == "" {
				continue
			}
			counts[root]++
			total++
		}
	}
	if total < 3 {
		return "" // too little evidence to override anything
	}
	best, n := "", 0
	for r, c := range counts {
		if c > n {
			best, n = r, c
		}
	}
	if float64(n) < 0.6*float64(total) {
		return ""
	}
	segs := strings.Split(strings.Trim(best, string(filepath.Separator)), string(filepath.Separator))
	if len(segs) >= 2 {
		return filepath.Join(segs[len(segs)-2], segs[len(segs)-1])
	}
	return filepath.Base(best)
}

// agentScratch drops the agent's own working area. A scratch clone under
// ~/.claude can hold more edits than the repository it was cloned from, and
// naming a session after it would be worse than the directory-derived name it
// replaces — measured: one session came out as `scratchpad/deja-push` instead
// of `goprojects/deja-vu`.
// The markers name the agent's own areas rather than a location. Excluding all
// of /tmp would be simpler and wrong: a repository can live there, and it is
// still that person's project.
func agentScratch(p string) bool {
	for _, seg := range []string{"/.claude/", "/scratchpad/", "/tasks/", "/.cache/", "/claude-501/"} {
		if strings.Contains(p, seg) {
			return true
		}
	}
	return false
}

var repoRootCache sync.Map

// repoRoot walks up until it finds a .git, which is what makes a directory a
// project rather than a folder. Cached because one session touches the same few
// directories hundreds of times.
func repoRoot(dir string) string {
	if dir == "" || dir == "/" {
		return ""
	}
	if v, ok := repoRootCache.Load(dir); ok {
		return v.(string)
	}
	root := ""
	for d := dir; d != "/" && d != "." && d != ""; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			root = d
			break
		}
	}
	repoRootCache.Store(dir, root)
	return root
}
