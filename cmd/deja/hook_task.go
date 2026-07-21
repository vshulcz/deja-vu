package main

import (
	"context"
	"os/exec"
	"path/filepath"
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

// changedTaskFiles returns basenames of files the repo is actively touching:
// uncommitted changes first, then files from the last few commits. Best
// effort — outside a repo, or with git missing or slow, it returns nil and
// recall falls back to recency.
func changedTaskFiles(cwd string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
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
		var text strings.Builder
		text.WriteString(strings.ToLower(s.Title))
		for _, m := range s.Messages {
			text.WriteString(" ")
			text.WriteString(strings.ToLower(m.Text))
		}
		low := text.String()
		n := 0
		for _, f := range files {
			if strings.Contains(low, f) {
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
