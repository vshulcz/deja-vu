package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// The home screen opens on the work in progress: the files changed and not
// yet committed in this repository, and the sessions that worked on them.
// It is blame run over the diff, the question someone sitting down at a
// half-finished change asks first.

const (
	behindFiles    = 6
	behindSessions = 3
)

// behindRow is one session behind the uncommitted change, with the file it
// was found through.
type behindRow struct {
	s    model.Session
	file string
}

// changedFiles lists the files with uncommitted changes under cwd, relative
// to it. No git, no repository or a slow one is an empty answer.
func changedFiles(cwd string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "diff", "--name-only", "--relative", "HEAD").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
		if len(files) == behindFiles {
			break
		}
	}
	return files
}

// tuiBehind blames each changed file and keeps the best session for each,
// one row per session.
func tuiBehind(dir, cwd string, files []string) []behindRow {
	var out []behindRow
	seen := map[string]bool{}
	for _, f := range files {
		target, err := search.ResolveBlamePath(filepath.Join(cwd, f))
		if err != nil {
			continue
		}
		result, err := index.SearchWithRecoveryDetailed(dir, search.Options{Query: target.Stem, All: true}, io.Discard)
		if err != nil {
			continue
		}
		hits := policyFilterBlame(policy.ActivationSearch, search.Blame(withFileTouchers(dir, result.Sessions, target), target, search.BlameOptions{}))
		// Another project's file of the same name is not behind this change.
		scope := blameScope(cwd, target, "", false)
		for _, h := range hits {
			if len(scope) > 0 && !howProjectMatches(h.Session.Project, scope) {
				continue
			}
			k := sessionKey(h.Session)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, behindRow{s: h.Session, file: f})
			break
		}
		if len(out) == behindSessions {
			break
		}
	}
	return out
}

// loadBehind works out the block off the main loop and rebuilds the home
// list when it lands.
func (a *tuiApp) loadBehind() {
	files := changedFiles(a.cwd)
	rows := tuiBehind(a.dir, a.cwd, files)
	a.post(func() {
		a.behind = rows
		if len(a.query) == 0 && a.scope != scopeKept && a.view == viewList {
			top := a.sel == 0
			a.loadHome()
			// The block lands over the list; a reader still at the top
			// stays at the top, on it.
			if top {
				a.sel, a.scroll = 0, 0
			}
		}
	})
}

// withBehind puts the block over the recent list, each session once.
func withBehind(behind []behindRow, recent []tuiRow, recentLabel string) []tuiRow {
	if len(behind) == 0 {
		return recent
	}
	var rows []tuiRow
	seen := map[string]bool{}
	for i, b := range behind {
		r := tuiRow{s: b.s, file: b.file}
		if i == 0 {
			r.section = "Behind your uncommitted change"
		}
		rows = append(rows, r)
		seen[sessionKey(b.s)] = true
	}
	first := true
	for _, r := range recent {
		if seen[sessionKey(r.s)] {
			continue
		}
		if first {
			r.section, first = recentLabel, false
		}
		rows = append(rows, r)
	}
	return rows
}
