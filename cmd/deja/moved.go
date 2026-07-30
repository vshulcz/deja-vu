package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A recalled session says what was discussed. What it cannot say by itself is
// whether the ground has moved since — and that is the objection deja gets
// every time it is discussed publicly.
//
// The version of this in #531 wanted to name *the* file a decision was about
// and warn on it. Measured on 551 sessions, the linkage is good enough to be
// interesting and not good enough to be authoritative: 37% of sessions record
// no path at all, 28% name a file that no longer exists on disk, and picking
// the single most-touched file is right about half the time by hand-check.
//
// So the claim is rewritten to one that needs no guessing and is true by
// construction: of the files this session touched, this many have commits since
// it ended. No claim is made about which file mattered, and — deliberately — no
// claim is made that anything is *unchanged*, because silence here means "we
// could not tell" far more often than it means "nothing moved".
const (
	// movedFileBudget bounds how many files one hit will ask git about. The
	// median session touches a handful; the largest in this corpus touched
	// 1,397, and asking about all of them would cost a minute per search.
	movedFileBudget = 6
	// movedGitBudget is the whole annotation's wall-clock allowance. Search
	// itself is ~2 ms, so this is by far the expensive part and it has to be
	// bounded rather than merely fast — the lesson from #516.
	movedGitBudget = 600 * time.Millisecond
	// movedHitBudget is one hit's share of it.
	// One cold git invocation on a large repository does not finish in 200 ms,
	// and a budget that quietly produces nothing is worse than no annotation.
	movedHitBudget = 400 * time.Millisecond
	// movedRepoBudget bounds how many repositories one hit will ask about. A
	// session that ranged over five checkouts is not one whose files a footnote
	// can summarise anyway.
	movedRepoBudget = 2
	// movedMinAge is how old a session must be before the question is worth
	// asking at all.
	movedMinAge = 12 * time.Hour
)

type movedReport struct {
	Changed int // files with commits since the session
	Looked  int // files actually asked about
}

// filesMovedSince counts how many of a session's files have commits after it
// ended. One git invocation per repository, not per file: asking about six
// files separately cost half a second on a command whose search takes thirty
// milliseconds, which is not a price a reader agreed to pay for a footnote.
//
// It is deliberately conservative: a file it cannot resolve, cannot stat, or
// that no longer exists is not counted either way.
func filesMovedSince(paths []string, since time.Time, budget time.Duration) movedReport {
	if since.IsZero() || len(paths) == 0 {
		return movedReport{}
	}
	// Nothing can have been committed after a session that ended minutes ago,
	// and the hits a reader sees first are usually today's. Skipping those
	// keeps the common search exactly as fast as it was.
	if time.Since(since) < movedMinAge {
		return movedReport{}
	}
	byRepo := map[string][]string{}
	var r movedReport
	for _, p := range paths {
		if len(byRepo) > movedRepoBudget {
			break
		}
		fi, err := os.Stat(p)
		if err != nil {
			// Gone or unreadable. It may have been renamed, or the whole
			// checkout may live somewhere else now; either way this is not
			// something to make a claim about.
			continue
		}
		// A file older on disk than the session cannot have changed since it,
		// and answering that costs a stat rather than a fork. A checkout resets
		// mtime and sends a file down the git path unnecessarily, which costs
		// time and never produces a wrong answer.
		if !fi.ModTime().After(since) {
			r.Looked++
			continue
		}
		root := gitRootOf(p)
		if root == "" {
			continue
		}
		if len(byRepo[root]) >= movedFileBudget {
			continue
		}
		byRepo[root] = append(byRepo[root], p)
		r.Looked++
	}
	if len(byRepo) == 0 {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	for root, files := range byRepo {
		args := append([]string{"-C", root, "log", "--format=", "--name-only",
			"--since=" + since.UTC().Format(time.RFC3339), "--"}, files...)
		out, err := exec.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				seen[line] = true
			}
		}
		r.Changed += len(seen)
	}
	return r
}

func gitRootOf(p string) string {
	for d := filepath.Dir(p); d != "." && d != ""; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
	return ""
}

// movedNote is the line printed under a hit, or "" when there is nothing
// honest to say.
func movedNote(r movedReport) string {
	if r.Changed == 0 {
		return ""
	}
	if r.Changed == 1 {
		return "  1 file this session touched has changed since"
	}
	return fmt.Sprintf("  %d files this session touched have changed since", r.Changed)
}

// attachMoved annotates the hits that are actually going to be read. Only the
// first few: the annotation costs a git call per file, and a reader who scrolls
// past hit ten is not waiting on this line.
func attachMoved(hits []search.Hit) {
	if len(hits) == 0 || os.Getenv("DEJA_MOVED") == "0" {
		return
	}
	// Per hit, not shared: the first hit is often the longest session in the
	// store, and a single budget for all of them let it spend the whole
	// allowance on its own files while later hits — the ones a reader is
	// comparing it against — silently got nothing.
	deadline := time.Now().Add(movedGitBudget)
	for i := range hits {
		if i >= movedAnnotatedHits || time.Now().After(deadline) {
			return
		}
		paths := hits[i].Session.Touched
		if len(paths) == 0 {
			continue
		}
		since := hits[i].Session.Updated
		hits[i].Moved = movedNote(filesMovedSince(paths, since, movedHitBudget))
	}
}

// movedAnnotatedHits is how many hits get the annotation at all.
const movedAnnotatedHits = 3
