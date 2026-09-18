package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The attribution rule, which is the whole of this surface: the session has to
// have written one of the lines the commit removed. Measured over 494 commits
// of this repository, time-and-file overlap attributed 2 of 4 dependency bumps
// to a session that had nothing to do with them and this rule attributed none
// (#1181).
func TestAttributeLineTakesTheSessionThatWroteTheRemovedLine(t *testing.T) {
	target := search.BlameTarget{FullPath: "/work/app/internal/pool.go", Base: "pool.go", Stem: "pool", Line: 12}
	commit := lineCommit{SHA: "abc1234567", Subject: "fix: one pool per shard", When: time.Now()}
	removed := map[string]bool{
		"conns := pool.New(shards * connsPerShard)": true,
	}
	wrote := model.Session{
		Harness: "claude", ID: "wrote", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "make the pool per shard", Time: commit.When.Add(-2 * time.Hour)},
			{Role: sources.RoleEdit, Text: "/work/app/internal/pool.go\nconns := pool.New(shards * connsPerShard)", Time: commit.When.Add(-time.Hour)},
		},
	}
	// The session that only read the file: a `files` record is not an edit, and
	// reading a line is not writing it.
	read := model.Session{
		Harness: "claude", ID: "read", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleFiles, Text: "/work/app/internal/pool.go", Time: commit.When.Add(-30 * time.Minute)},
		},
	}
	// And one that edited the same file after the commit, which cannot be what
	// the commit replaced.
	later := model.Session{
		Harness: "claude", ID: "later", Project: "app",
		Messages: []model.Message{
			{Role: sources.RoleEdit, Text: "/work/app/internal/pool.go\nconns := pool.New(shards * connsPerShard)", Time: commit.When.Add(time.Hour)},
		},
	}

	got, ok := attributeLine([]model.Session{read, later, wrote}, target, commit, removed)
	if !ok || got.Session.ID != "wrote" {
		t.Fatalf("attributed to %q (ok=%v), want the session that wrote the line", got.Session.ID, ok)
	}
	if !strings.Contains(got.Matched, "connsPerShard") {
		t.Errorf("the matched line is not reported: %q", got.Matched)
	}
	if got.Asked == "" {
		t.Error("the session's own task is what orients a reader; it is missing")
	}

	// The control: a commit whose removed lines nobody wrote — a dependency
	// bump, a merge, a hand edit deja never saw — attributes to nothing.
	if _, ok := attributeLine([]model.Session{read, later, wrote}, target, commit, map[string]bool{
		"uses: actions/checkout@v7.0.1": true,
	}); ok {
		t.Error("a commit deja never saw must attribute to nothing")
	}
	// And a worktree checkout of the same repository is the same file.
	wt := search.BlameTarget{FullPath: "/work/app/.claude/worktrees/x/internal/pool.go", Base: "pool.go", Stem: "pool", Line: 12}
	if _, ok := attributeLine([]model.Session{wrote}, wt, commit, removed); !ok {
		t.Error("the same file in a worktree checkout should still match")
	}
}

// A line is evidence only if it could have been written on purpose. Braces and
// `return nil` are in every diff and in every session.
func TestBlameSpanKeyDropsLinesThatMatchEverything(t *testing.T) {
	for _, s := range []string{"}", "\t}", "return nil", "  return err", "})", "import ("} {
		if blameSpanKey(s) != "" {
			t.Errorf("%q matches every commit and must not count", s)
		}
	}
	if got := blameSpanKey("   conns := pool.New(shards * connsPerShard)  "); got != "conns := pool.New(shards * connsPerShard)" {
		t.Errorf("got %q", got)
	}
}

// End to end against a real repository: git for the line's commit, the store
// for the session that wrote what it replaced.
func TestBlameLineNamesTheSessionBehindTheLine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := hermeticEnv(t)
	repo := filepath.Join(tmp, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	file := filepath.Join(repo, "pool.go")
	const before = "package app\n\nvar conns = newPool(shardCount * connsPerShard)\n"
	const after = "package app\n\nvar conns = newPool(shardCount)\n"
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "pool.go")
	git("commit", "-qm", "first")
	if err := os.WriteFile(file, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-aqm", "fix: one connection per shard is enough")

	// The session that wrote the line the second commit replaced.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "aaaa1111-2222-4000-8000-c3d4e5f6a7b8"
	stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	// Encoded rather than pasted: a Windows path carries backslashes, and a
	// path glued into a JSON fixture by hand is this repository's most frequent
	// windows-only failure.
	var b strings.Builder
	for _, rec := range []map[string]any{
		{"type": "user", "sessionId": id, "cwd": repo, "timestamp": stamp,
			"message": map[string]any{"role": "user", "content": "size the pool by shard"}},
		{"type": "assistant", "sessionId": id, "cwd": repo, "timestamp": stamp,
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "name": "Edit", "input": map[string]any{
					"file_path":  file,
					"old_string": "var conns = newPool(shardCount * connsPerShard)",
					"new_string": "var conns = newPool(shardCount)",
				}},
			}}},
	} {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "blame", file+":3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pool.go:3 last changed in") {
		t.Fatalf("the line's own commit is not named:\n%s", out)
	}
	if !strings.Contains(out, "written in claude") || !strings.Contains(out, id[:12]) {
		t.Fatalf("the session that wrote the replaced line is not named:\n%s", out)
	}
	if !strings.Contains(out, "connsPerShard") {
		t.Fatalf("the replaced line is not shown:\n%s", out)
	}

	// The control, in the same repository: a commit whose removed lines no
	// session wrote must say so rather than name the nearest session.
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("# app\n\nrun it with make, and read docs/pool.md first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-qm", "docs: a readme")
	if err := os.WriteFile(readme, []byte("# app\n\nrun it with make, and read docs/shards.md first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-aqm", "chore(deps): bump the docs link")
	out, err = captureRun(t, "blame", readme+":3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no indexed session wrote the lines this commit replaced") {
		t.Fatalf("a commit deja never saw must attribute to nothing:\n%s", out)
	}
}
