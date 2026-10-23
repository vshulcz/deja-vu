package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// aliasedCheckouts indexes one answering session in checkout a and sixty
// sessions elsewhere that repeat the query's words, then returns checkout b,
// which has no sessions of its own. With alias set, a and b are listed in the
// alias file as one project named "mono".
func aliasedCheckouts(t *testing.T, alias bool) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	work := t.TempDir()
	a := filepath.Join(work, "mono", "svc", "ledgerd")
	b := filepath.Join(work, "trees", "task-7", "svc", "billing")
	other := filepath.Join(work, "src", "other")
	for _, d := range []string{a, b, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if alias {
		body := fmt.Sprintf("%s = mono\n%s = mono\n", filepath.Join(work, "mono"), filepath.Join(work, "trees"))
		if err := os.MkdirAll(filepath.Dir(sources.ProjectAliasPath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sources.ProjectAliasPath(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := func(sid, cwd, role, text string) string {
		return string(mustMarshal(t, map[string]any{"type": role, "sessionId": sid, "cwd": cwd, "timestamp": "2026-09-10T10:00:00Z",
			"message": map[string]any{"role": role, "content": text}}))
	}
	writeClaudeFixture(t, filepath.Join(root, claudeFolderFor(a), "dana.jsonl"), "dana", []string{
		line("dana", a, "user", "who owns the bulk import now that Theo is gone"),
		line("dana", a, "assistant", "pgx and bulk-import reviews go to Dana Whitfield, not Theo Brandt."),
	})
	for i := range 60 {
		sid := fmt.Sprintf("o%02d", i)
		writeClaudeFixture(t, filepath.Join(root, claudeFolderFor(other), sid+".jsonl"), sid, []string{
			line(sid, other, "user", fmt.Sprintf("service %d: open the pgx upgrade PR %d and find a reviewer; the owner wants review today", i, 100+i)),
			line(sid, other, "assistant", fmt.Sprintf("PR %d pgx upgrade: reviewer assigned by the owner of service %d, review requested, pgx upgrade review pending", 100+i, i)),
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return b
}

// claudeFolderFor is the folder Claude Code files a directory's transcripts
// under: every character outside [A-Za-z0-9] written as "-".
func claudeFolderFor(dir string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
}

// Work spread over several checkouts of one thing — a monorepo and a worktree
// per task — was a project per checkout, so recall standing in a fresh
// worktree never put the decision made in the main checkout first. Listed in
// the alias file, the checkouts are one project.
func TestRecallTreatsAliasedCheckoutsAsOneProject(t *testing.T) {
	const q = `{"mode":"recall","q":"pgx upgrade PR reviewer owner review"}`
	firstRow := regexp.MustCompile(`\n1\. \[[^\]]*\] (\S+)`)
	ask := func(t *testing.T) (string, string) {
		t.Helper()
		text, err := callMCPTool(index.DefaultDir(), "deja", json.RawMessage(q))
		if err != nil {
			t.Fatal(err)
		}
		m := firstRow.FindStringSubmatch(text)
		if m == nil {
			return "", text
		}
		return m[1], text
	}

	t.Run("aliased", func(t *testing.T) {
		t.Chdir(aliasedCheckouts(t, true))
		first, text := ask(t)
		if first != "mono" || !strings.Contains(text, "Dana") {
			t.Fatalf("from the other checkout, the aliased project's answer is not first:\n%s", firstLines(text, 12))
		}
	})
	// Control: without the file the second checkout is a project with no
	// sessions, and the sixty win.
	t.Run("not aliased", func(t *testing.T) {
		t.Chdir(aliasedCheckouts(t, false))
		if first, text := ask(t); first == "" || strings.HasSuffix(first, "ledgerd") {
			t.Fatalf("ledgerd led without an alias, so the case above proves nothing:\n%s", firstLines(text, 12))
		}
	})
}

// The prompt hook ranks only inside the project it stands in, so without the
// alias a question asked in the second checkout heard nothing of the first.
func TestPromptHookRecallsAcrossAliasedCheckouts(t *testing.T) {
	hook := func(t *testing.T, cwd string) string {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"prompt": "who owns the bulk import reviews now?", "cwd": cwd, "session_id": "now"})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	t.Run("aliased", func(t *testing.T) {
		if out := hook(t, aliasedCheckouts(t, true)); !strings.Contains(out, "Dana") {
			t.Fatalf("the hook in the second checkout did not recall the first:\n%s", out)
		}
	})
	t.Run("not aliased", func(t *testing.T) {
		if out := hook(t, aliasedCheckouts(t, false)); strings.Contains(out, "Dana") {
			t.Fatalf("the hook recalled another project without an alias, so the case above proves nothing:\n%s", out)
		}
	})
}

// aliasFile writes the alias file naming the two trees aliasedCheckouts made
// as one project, after the index was built.
func aliasFile(t *testing.T, b string) string {
	t.Helper()
	work := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(b))))
	a := filepath.Join(work, "mono", "svc", "ledgerd")
	body := fmt.Sprintf("%s = mono\n%s = mono\n", filepath.Join(work, "mono"), filepath.Join(work, "trees"))
	if err := os.MkdirAll(filepath.Dir(sources.ProjectAliasPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sources.ProjectAliasPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return a
}

func promptHook(t *testing.T, cwd, session string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"prompt": "who owns the bulk import reviews now?", "cwd": cwd, "session_id": session})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Writing the file renamed the checkout for the hooks before the index was
// rebuilt, and its own sessions, still filed under the old name, dropped out
// of recall there (#5373).
func TestHookInACheckoutStillRecallsItsSessionsIndexedBeforeTheAlias(t *testing.T) {
	b := aliasedCheckouts(t, false)
	a := aliasFile(t, b)
	if out := promptHook(t, a, "s1"); !strings.Contains(out, "Dana") {
		t.Fatalf("after writing the alias file the checkout lost its own sessions:\n%s", out)
	}
	// The other checkout joins only after the rebuild.
	if _, err := captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	// What the hook served the first checkout is not served again.
	if err := os.Remove(index.DefaultDir() + ".hookseen"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if out := promptHook(t, b, "s2"); !strings.Contains(out, "Dana") {
		t.Fatalf("after the rebuild the second checkout does not recall the first:\n%s", out)
	}
}

// `deja index` says a rebuild is due after the file changes, and does not
// start one (#5373).
func TestIndexSaysAnAliasEditNeedsARebuild(t *testing.T) {
	b := aliasedCheckouts(t, false)
	out, err := captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "changed since this index was built") {
		t.Fatalf("no alias file, yet index asks for a rebuild:\n%s", out)
	}
	aliasFile(t, b)
	out, err = captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "projects changed since this index was built") || !strings.Contains(out, "deja index --rebuild") {
		t.Errorf("index said nothing about the alias edit:\n%s", out)
	}
	// Not started: the session is still under its old name.
	if hooked := promptHook(t, b, "s1"); strings.Contains(hooked, "Dana") {
		t.Errorf("the index applied the alias without a rebuild:\n%s", hooked)
	}
	doc, _ := captureRun(t, "doctor")
	if !strings.Contains(doc, "projects") || !strings.Contains(doc, "sessions already indexed keep their old names") {
		t.Errorf("doctor said nothing about the alias edit:\n%s", doc)
	}
	if _, err = captureRunStderr(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRunStderr(t, "index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "changed since this index was built") {
		t.Errorf("index still asks for a rebuild it already got:\n%s", out)
	}
	doc, _ = captureRun(t, "doctor")
	if !strings.Contains(doc, "2 trees named in") {
		t.Errorf("doctor does not list the trees:\n%s", doc)
	}
}

// The exclude list wins over the alias: a repository excluded by its own name
// stays out of the index once the tree it sits in is aliased (#5373).
func TestAnExcludedRepoStaysOutUnderAnAlias(t *testing.T) {
	t.Run("excluded", func(t *testing.T) {
		t.Setenv("DEJA_EXCLUDE_PROJECTS", "svc/ledgerd")
		aliasedCheckouts(t, true)
		if hits, _ := captureRun(t, "Dana Whitfield"); strings.Contains(hits, "Dana") {
			t.Errorf("the excluded repository was indexed under the alias:\n%s", hits)
		}
	})
	// Control: without the pattern the same build indexes it.
	t.Run("not excluded", func(t *testing.T) {
		t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
		aliasedCheckouts(t, true)
		if hits, _ := captureRun(t, "Dana Whitfield"); !strings.Contains(hits, "Dana") {
			t.Fatalf("the session is not found even without the pattern, so the case above proves nothing:\n%s", hits)
		}
	})
}
