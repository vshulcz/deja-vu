package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Task-aware session-start recall: there is no prompt at session start, but
// the repository says what the work is about. Sessions that mention the files
// the working tree is touching outrank plain recency.

const taskFileCap = 8

var taskNoiseFiles = map[string]bool{
	"go.sum": true, "go.mod": true, "package-lock.json": true,
	"yarn.lock": true, "pnpm-lock.yaml": true, "cargo.lock": true,
	"gemfile.lock": true, "poetry.lock": true, "uv.lock": true,
}

// taskGitBudget is how long the two git calls together may take before recall
// gives up on file ranking. It is deliberately short: this runs on the hook
// path, in front of an agent that is waiting.
//
// A variable rather than a constant because a slow machine — a cold Windows
// runner was the first one seen — blows through it and both calls return
// nothing, which is indistinguishable from "no repo" and made the test for
// this function flaky (#516). The tests answered that by widening it to 20s,
// which left the product shipping the number that misses: on windows the two
// calls return nothing and recall ranks by recency instead of by the files the
// repository is touching, silently. Platform budget, same as the root lookup
// (#3624) and the compaction fingerprint (#3645).
var taskGitBudget = taskBudgetFor(runtime.GOOS)

func taskBudgetFor(goos string) time.Duration {
	if goos == "windows" {
		return 2 * time.Second
	}
	return 400 * time.Millisecond
}

// changedTaskFiles returns basenames of files the repo is actively touching:
// uncommitted changes first, then files from the last few commits. Best
// effort — outside a repo, or with git missing or slow, it returns nil and
// recall falls back to recency.
func changedTaskFiles(cwd string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), taskGitBudget)
	defer cancel()
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		base := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
		if base == "" || base == "." || taskNoiseFiles[base] || strings.HasSuffix(base, ".lock") {
			return
		}
		if !strings.Contains(base, ".") {
			return // directories and extensionless noise carry little signal
		}
		if !seen[base] && len(out) < taskFileCap {
			seen[base] = true
			out = append(out, base)
		}
	}
	if b, err := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if len(line) < 4 {
				continue
			}
			path := line[3:]
			if i := strings.Index(path, " -> "); i >= 0 {
				path = path[i+4:]
			}
			add(path)
		}
	}
	if b, err := exec.CommandContext(ctx, "git", "-C", cwd, "log", "--name-only", "-3", "--pretty=format:").Output(); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			add(line)
		}
	}
	return out
}

// taskScores counts, per session, how many of the changed files it mentions.
// The returned matched list holds the files that drove the ranking, most
// mentioned first, for the receipt line.
func taskScores(ss []model.Session, files []string) (map[string]int, []string) {
	if len(files) == 0 {
		return nil, nil
	}
	scores := map[string]int{}
	fileHits := map[string]int{}
	for _, s := range ss {
		found := sessionMentions(s, files)
		n := 0
		for i, f := range files {
			if found[i] {
				n++
				fileHits[f]++
			}
		}
		if n > 0 {
			scores[s.Harness+":"+s.ID] = n
		}
	}
	if len(scores) == 0 {
		return nil, nil
	}
	matched := make([]string, 0, len(fileHits))
	for f := range fileHits {
		matched = append(matched, f)
	}
	sort.Slice(matched, func(i, j int) bool {
		if fileHits[matched[i]] != fileHits[matched[j]] {
			return fileHits[matched[i]] > fileHits[matched[j]]
		}
		return matched[i] < matched[j]
	})
	if len(matched) > 3 {
		matched = matched[:3]
	}
	return scores, matched
}

// sessionMentions reports, per file, whether the session's lowered title and
// messages joined by spaces contain it. A name without a space cannot match
// across a join, so each message is searched on its own and on every core: the
// joined copy of a marathon session was most of a session start's time.
func sessionMentions(s model.Session, files []string) []bool {
	found := make([]bool, len(files))
	for _, f := range files {
		if strings.Contains(f, " ") {
			var text strings.Builder
			text.WriteString(strings.ToLower(s.Title))
			for _, m := range s.Messages {
				text.WriteString(" ")
				text.WriteString(strings.ToLower(m.Text))
			}
			low := text.String()
			for i, f := range files {
				found[i] = strings.Contains(low, f)
			}
			return found
		}
	}
	mark := func(low string, into []bool) {
		for i, f := range files {
			if !into[i] && strings.Contains(low, f) {
				into[i] = true
			}
		}
	}
	mark(strings.ToLower(s.Title), found)
	k := len(files)
	per := make([]bool, len(s.Messages)*k)
	parallelChunks(len(s.Messages), func(i int) {
		mark(strings.ToLower(s.Messages[i].Text), per[i*k:(i+1)*k])
	})
	for i, h := range per {
		found[i%k] = found[i%k] || h
	}
	return found
}
