package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An agent isolated in a Claude Code worktree edits the repository's own
// source under <repo>/.claude/worktrees/<name>/. Those paths were dropped as the
// agent's bookkeeping, so the session had no Touched files and blame never
// listed it (#4164). The scratch files under .claude/ still stay out.
func TestFilesInAClaudeWorktreeCountAsTouched(t *testing.T) {
	wt := "/repo/.claude/worktrees/agent-a1/apps/promptidea/idea.go"
	ms := []model.Message{
		{Role: roleFiles, Text: wt},
		{Role: roleFiles, Text: wt},
		{Role: roleFiles, Text: "/Users/x/.claude/projects/p/notes.md"},
		{Role: roleFiles, Text: "/repo/.claude/worktrees/agent-a1/.git/HEAD"},
	}
	got := topTouchedFiles(ms)
	if len(got) != 1 || got[0] != wt {
		t.Fatalf("touched = %q, want only the worktree source file", got)
	}
}
