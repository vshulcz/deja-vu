package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/gitread"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// claudeMemoryBlock is what Claude Code wrote in its auto-memory for this
// project, for an agent that is not Claude. Claude loads that memory itself
// at every start; Codex, opencode and the rest never see it, so a rule the
// user taught one agent ("no Co-Authored-By trailers here") had to be taught
// again to each. Rules first, then what it knows about the person, then
// facts; newest first within each, the lines the person set aside left out.
func claudeMemoryBlock(dir, cwd string, maxLines, budget int) string {
	if !memoryAllowed(cwd, policy.ActivationAuto) {
		return ""
	}
	mems := projectClaudeMemories(cwd)
	if len(mems) == 0 {
		return ""
	}
	hidden := memHidden(dir)
	rank := map[string]int{"feedback": 0, "user": 1, "project": 2, "reference": 3}
	var picked []sources.AgentMemory
	for _, m := range mems {
		if !hidden[m.Path] && strings.TrimSpace(m.Description) != "" {
			picked = append(picked, m)
		}
	}
	// ClaudeMemories is newest first; a stable sort keeps that inside a type.
	order := func(t string) int {
		if r, ok := rank[t]; ok {
			return r
		}
		return len(rank)
	}
	sort.SliceStable(picked, func(i, j int) bool { return order(picked[i].Type) < order(picked[j].Type) })
	var b strings.Builder
	b.WriteString("what Claude Code learned in this project (its own memory, written in earlier sessions here; it applies to you too unless the user overrides):\n")
	head := b.Len()
	shown := 0
	for _, m := range picked {
		if shown >= maxLines {
			break
		}
		row := "  · " + search.SafeLine(strings.TrimSpace(m.Description)) + "\n"
		if b.Len()+len(row) > budget {
			continue
		}
		b.WriteString(row)
		shown++
	}
	if b.Len() == head {
		return ""
	}
	if left := len(picked) - shown; left > 0 {
		b.WriteString("  (" + num(left) + " more: deja's Memory tab lists them)\n")
	}
	return b.String()
}

// memoryAllowed asks the trust policy and the exclude list about every name
// the directory goes by, the way the session digest does: a project withheld
// from an activation must not hand on its memory through it either.
func memoryAllowed(cwd, activation string) bool {
	pol := policy.Load()
	if pol.Ignored(cwd, cwd) {
		return false
	}
	for _, n := range digest.ProjectNameCandidates(cwd) {
		if !pol.Allows(activation, n) || sources.ExcludedProject(n) {
			return false
		}
	}
	return true
}

// projectClaudeMemories reads the memory for cwd, and only when there is
// none asks git for the main checkout: the fork costs more than the read.
func projectClaudeMemories(cwd string) []sources.AgentMemory {
	if m := sources.ClaudeMemories(cwd); len(m) > 0 || cwd == "" {
		return m
	}
	return sources.ClaudeMemories(memoryCheckouts(cwd)...)
}

// memoryCheckouts is the directory and, in a linked worktree, the main
// checkout it belongs to: Claude keeps one memory per directory it started
// in, and a worktree is usually not where it was started.
func memoryCheckouts(cwd string) []string {
	if cwd == "" {
		return nil
	}
	out := []string{cwd}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := gitread.Command(ctx, "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	b, err := cmd.Output()
	if err != nil {
		return out
	}
	common := strings.TrimSpace(string(b))
	if filepath.Base(common) == ".git" {
		if main := filepath.Dir(common); main != filepath.Clean(cwd) {
			out = append(out, main)
		}
	}
	return out
}
