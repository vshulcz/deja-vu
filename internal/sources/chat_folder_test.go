package sources

import (
	"path/filepath"
	"testing"
)

// Every WorkBuddy chat under one workspace root is one project (#4735); a
// folder a user named is still its own.
func TestChatFolderNamesItsWorkspaceRoot(t *testing.T) {
	for cwd, want := range map[string]string{
		"/Users/a/WorkBuddy AI/2026-10-05-15-06-37":            "a/WorkBuddy AI",
		"/Users/a/WorkBuddy AI/2026-10-05-15-06-37/":           "a/WorkBuddy AI",
		`C:\Users\a\WorkBuddy\2026-10-05-15-06-37`:             "a/WorkBuddy",
		"/Users/a/coding/deja-vu/.claude/worktrees/agent-a1b2": "coding/deja-vu",
		`C:\Users\a\deja-vu\.claude\worktrees\agent-a1b2`:      "a/deja-vu",
		"/Users/a/src/2026-10-05":                              "src/2026-10-05",
		"/Users/a/src/app":                                     "src/app",
		"/2026-10-05-15-06-37":                                 "2026-10-05-15-06-37",
		`C:\2026-10-05-15-06-37`:                               "2026-10-05-15-06-37",
	} {
		if got := cwdProjectName(cwd); got != want {
			t.Errorf("cwdProjectName(%q) = %q, want %q", cwd, got, want)
		}
	}
	if got := decodeProjectBase("-Users-a-goprojects-repo--claude-worktrees-pin-manifest"); got != "goprojects/repo" {
		t.Errorf("decodeProjectBase(worktree) = %q, want goprojects/repo", got)
	}
	wtRoot, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/goprojects/deja-vu/.claude/worktrees/fix-5761"))
	if !ok || wtRoot != filepath.FromSlash("/home/a/goprojects/deja-vu") {
		t.Fatalf("ChatWorkspaceRoot(worktree) = %q %v", wtRoot, ok)
	}
	root, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/WorkBuddy AI/2026-10-05-15-06-37"))
	if !ok || root != filepath.FromSlash("/home/a/WorkBuddy AI") {
		t.Fatalf("ChatWorkspaceRoot = %q %v", root, ok)
	}
	if _, ok := ChatWorkspaceRoot(filepath.FromSlash("/home/a/app")); ok {
		t.Fatal("a folder the user named was taken for a chat folder")
	}
	// A lone timestamp folder at a filesystem root has no root of its own.
	for _, cwd := range []string{"/2026-10-05-15-06-37", `C:\2026-10-05-15-06-37`} {
		if root, ok := ChatWorkspaceRoot(filepath.FromSlash(cwd)); ok {
			t.Errorf("ChatWorkspaceRoot(%q) = %q, want none", cwd, root)
		}
	}
}
