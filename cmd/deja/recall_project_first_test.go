package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The prompt hook ranks inside the project the agent works in and recall
// ranked the whole machine, so an agent that asked recall itself got another
// project's sessions above its own project's answer. Here sixty sessions from
// elsewhere say the query's words over and over, and the one session of this
// project that answers says them once.
func TestRecallPutsThisProjectsAnswerFirst(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	ledgerd := filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(ledgerd, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, cwd, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(ledgerd), "dana.jsonl"), "dana", []string{
		line("dana", ledgerd, "user", "who owns the bulk import now that Theo is gone"),
		line("dana", ledgerd, "assistant", "pgx and bulk-import reviews go to Dana Whitfield, not Theo Brandt."),
	})
	other := filepath.Join(work, "src", "other")
	for i := range 60 {
		sid := fmt.Sprintf("o%02d", i)
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(other), sid+".jsonl"), sid, []string{
			line(sid, other, "user", fmt.Sprintf("service %d: open the pgx upgrade PR %d and find a reviewer; the owner wants review today", i, 100+i)),
			line(sid, other, "assistant", fmt.Sprintf("PR %d pgx upgrade: reviewer assigned by the owner of service %d, review requested, pgx upgrade review pending", 100+i, i)),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	firstRow := regexp.MustCompile(`\n1\. \[[^\]]*\] (\S+)`)
	ask := func(args string) string {
		t.Helper()
		text, err := callMCPTool(dir, "deja", json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	const q = `{"mode":"recall","q":"pgx upgrade PR reviewer owner review"}`

	t.Chdir(ledgerd)
	text := ask(q)
	m := firstRow.FindStringSubmatch(text)
	if len(m) < 2 || !strings.HasSuffix(m[1], "ledgerd") || !strings.Contains(text, "Dana") {
		t.Fatalf("from inside ledgerd, its own answer is not first:\n%s", firstLines(text, 12))
	}

	// Control: asked from a directory that is no project, the sixty win, so
	// the order above is the project's doing and not the fixture's.
	t.Chdir(t.TempDir())
	if m := firstRow.FindStringSubmatch(ask(q)); len(m) < 2 || strings.HasSuffix(m[1], "ledgerd") {
		t.Fatalf("outside any project ledgerd still led, so the test above proves nothing: %v", m)
	}

	// A project the caller names is a filter, as the tool describes it.
	t.Chdir(ledgerd)
	if named := ask(`{"mode":"recall","q":"pgx upgrade PR reviewer owner review","project":"src/other"}`); strings.Contains(named, "Dana") {
		t.Fatalf("project src/other still served ledgerd's session:\n%s", firstLines(named, 12))
	}
}

// Agents name the project by the directory they work in, and that is often a
// git worktree whose path no recorded session carries. Recall took the path
// as a name to find inside recorded paths and answered with nothing.
func TestRecallProjectNamedByAWorktreePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	repo := filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "init")
	tree := filepath.Join(work, "runs", "wt1")
	git("worktree", "add", "-q", "--detach", tree)

	line := func(sid, cwd, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(repo), "pay.jsonl"), "pay", []string{
		line("pay", repo, "user", "what do we call the new payouts service binary"),
		line("pay", repo, "assistant", "Every service binary is named ledger-<name>d, so payouts is ledger-payoutd."),
	})
	other := filepath.Join(work, "src", "other")
	writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(other), "o.jsonl"), "o", []string{
		line("o", other, "user", "rename the payouts service binary"),
		line("o", other, "assistant", "Renamed the payouts service binary to payd."),
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tree)
	args, err := json.Marshal(map[string]string{"mode": "recall", "q": "payouts service binary name", "project": tree})
	if err != nil {
		t.Fatal(err)
	}
	text, err := callMCPTool(dir, "deja", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ledger-payoutd") {
		t.Fatalf("project named by its worktree path lost the repo's session:\n%s", firstLines(text, 12))
	}
	if strings.Contains(text, "payd.") {
		t.Fatalf("project named by path still served another project's session:\n%s", firstLines(text, 12))
	}
}

// A question carrying words this project never said, but other projects did,
// read inside the project as one known word among typos, and the project's own
// answer went silent behind the rest of the machine (#4789).
func TestRecallKeepsThisProjectWhenItLacksSomeWords(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	ledgerd := filepath.Join(work, "src", "ledgerd")
	if err := os.MkdirAll(ledgerd, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, cwd, role, text string) string {
		b, err := json.Marshal(map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-14T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(ledgerd), "replay.jsonl"), "replay", []string{
		line("replay", ledgerd, "user", "how do we know a reconcile change is safe"),
		line("replay", ledgerd, "assistant", "Rule: every change in cmd/reconcile is checked with the dry-run replay of the August bank file; expect 4117 matched."),
	})
	other := filepath.Join(work, "src", "other")
	for i := range 12 {
		sid := fmt.Sprintf("k%02d", i)
		writeClaudeFixture(t, filepath.Join(root, sources.ClaudeProjectName(other), sid+".jsonl"), sid, []string{
			line(sid, other, "user", fmt.Sprintf("controller %d: raise the retry tolerance setting", i)),
			line(sid, other, "assistant", fmt.Sprintf("Set the tolerance setting to %d in the controller config and let it reconcile.", i)),
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ledgerd)
	text, err := callMCPTool(dir, "deja", json.RawMessage(`{"mode":"recall","q":"match.go tolerance reconcile setting"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`\n1\. \[[^\]]*\] (\S+)`).FindStringSubmatch(text); len(m) < 2 || !strings.HasSuffix(m[1], "ledgerd") || !strings.Contains(text, "4117") {
		t.Fatalf("this project's answer is not first:\n%s", firstLines(text, 14))
	}
}
