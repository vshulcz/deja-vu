package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
)

func initOriginTestRepo(t *testing.T) (string, func(env []string, args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	git := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=dev", "GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=dev", "GIT_COMMITTER_EMAIL=dev@example.com",
		)
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git(nil, "init", "-q")
	git(nil, "config", "user.name", "dev")
	git(nil, "config", "user.email", "dev@example.com")
	return repo, git
}

func TestOriginAttributesReplacedAndWroteAndRefusesCompactionSummaries(t *testing.T) {
	doc := jsonOutputDoc(t)
	hermeticEnv(t)
	repo, git := initOriginTestRepo(t)
	t.Chdir(repo)

	poolFile := filepath.Join(repo, "internal", "pool", "pool.go")
	oldLine := "\tconn.closeWithoutDrainingInflightRequests()\n"
	newLine := "\tconn.drainInflightRequestsBeforeClose()\n"

	// Commit 1: initial file added at 10:05.
	if err := os.WriteFile(poolFile, []byte("package pool\n\nfunc shutdown() {\n"+oldLine+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:05:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:05:00Z"}, "commit", "-qm", "feat(pool): initial shutdown")
	sha1 := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 2: replaces oldLine with newLine at 10:20.
	if err := os.WriteFile(poolFile, []byte("package pool\n\nfunc shutdown() {\n"+newLine+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:20:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:20:00Z"}, "commit", "-qm", "fix(pool): drain inflight requests before closing")
	sha2 := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 3: dependency bump nobody indexed at 10:30.
	goMod := filepath.Join(repo, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/myapp\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T10:30:00Z", "GIT_COMMITTER_DATE=2026-09-18T10:30:00Z"}, "commit", "-qm", "build(deps): bump module")

	projDir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), nonAlnum.ReplaceAllString(repo, "-"))
	// Session 1 wrote the initial file (Rule: wrote).
	writeClaudeFixture(t, filepath.Join(projDir, "sess-wrote.jsonl"), "sess-wrote", []string{
		fixtureRow("sess-wrote", "user", "2026-09-18T10:00:00Z", repo, "добавь первоначальный shutdown для пула соединений"),
		fixtureRow("sess-wrote", "assistant", "2026-09-18T10:02:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "w1", "name": "Write",
			"input": map[string]any{
				"file_path": poolFile,
				"content":   "package pool\n\nfunc shutdown() {\n" + oldLine + "}\n",
			},
		}}),
		fixtureToolResult("sess-wrote", "2026-09-18T10:02:05Z", repo, "w1", "File created.", false),
	})

	// Session 2 replaced oldLine with newLine (Rule: replaced), after a real instruction,
	// a compaction summary, and a multi-word nudge ("ок и давай дальше").
	compactionSummary := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."
	writeClaudeFixture(t, filepath.Join(projDir, "sess-edit.jsonl"), "sess-edit", []string{
		fixtureRow("sess-edit", "user", "2026-09-18T10:10:00Z", repo, "請修復連接池關閉時未排空請求的問題"),
		fixtureRow("sess-edit", "user", "2026-09-18T10:12:00Z", repo, compactionSummary),
		fixtureRow("sess-edit", "user", "2026-09-18T10:14:00Z", repo, "ок и давай дальше"),
		fixtureRow("sess-edit", "assistant", "2026-09-18T10:15:00Z", repo, "Root cause: shutdown closed the connection immediately without draining in-flight requests first."),
		fixtureRow("sess-edit", "assistant", "2026-09-18T10:15:30Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "e1", "name": "Edit",
			"input": map[string]any{
				"file_path":  poolFile,
				"old_string": strings.TrimSpace(oldLine),
				"new_string": strings.TrimSpace(newLine),
			},
		}}),
		fixtureToolResult("sess-edit", "2026-09-18T10:15:35Z", repo, "e1", "Updated.", false),
	})

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// 1. Single commit JSON check on sha2 (replaced rule).
	var buf bytes.Buffer
	if err := runOrigin(dir, []string{sha2, "--json"}, &buf); err != nil {
		t.Fatalf("runOrigin sha2 --json: %v", err)
	}
	var env originEnvelopeJSON
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if env.SchemaVersion != jsonout.Version || len(env.Commits) != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	c2 := env.Commits[0]
	if c2.Status != originStatusAttributed || !c2.Attributed || c2.Rule != lineRuleReplaced {
		t.Fatalf("commit 2 attribution = %+v", c2)
	}
	if c2.Session == nil || c2.Session.ID != "sess-edit" {
		t.Fatalf("session = %+v, want sess-edit", c2.Session)
	}
	// Compaction summary and bare nudge ("продолжай") must be skipped in favor of the real instruction.
	if c2.Prompt != "請修復連接池關閉時未排空請求的問題" {
		t.Errorf("prompt = %q, want the substantive user instruction", c2.Prompt)
	}
	if strings.Contains(c2.Prompt, "continued from a previous") || c2.Prompt == "продолжай" {
		t.Errorf("compaction summary or bare nudge leaked into prompt: %q", c2.Prompt)
	}
	if !strings.Contains(c2.SaidBefore, "Root cause: shutdown closed") {
		t.Errorf("said_before = %q", c2.SaidBefore)
	}
	if c2.TurnGap != "6m" || c2.WriteGap != "5m" {
		t.Errorf("gaps = turn:%q write:%q", c2.TurnGap, c2.WriteGap)
	}

	// 2. Range check over all 3 commits (sha1..HEAD -> sha3, sha2).
	buf.Reset()
	if err := runOrigin(dir, []string{sha1 + "..HEAD", "--json"}, &buf); err != nil {
		t.Fatalf("runOrigin range: %v", err)
	}
	env = originEnvelopeJSON{}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(env.Commits))
	}
	if env.Commits[0].Status != originStatusUnknown || env.Commits[0].Attributed {
		t.Errorf("bump commit should be unknown: %+v", env.Commits[0])
	}
	if env.Commits[1].Status != originStatusAttributed || env.Commits[1].Session.ID != "sess-edit" {
		t.Errorf("second commit in range = %+v", env.Commits[1])
	}

	// 3. Verify that the emitted JSON keys match docs/json-output.md section.
	section := docSection(t, doc, "## `deja origin [rev|range] --json`")
	var v any
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	emitted := map[string]bool{}
	collectJSONKeys(v, nil, nil, emitted)
	for _, k := range sortedSet(emitted) {
		if !namesKey(section, k) {
			t.Errorf("deja origin --json prints %q, and its section in docs/json-output.md never names it", k)
		}
	}

	// 4. Prose output on sha1 (wrote rule).
	buf.Reset()
	if err := runOrigin(dir, []string{sha1}, &buf); err != nil {
		t.Fatal(err)
	}
	prose := buf.String()
	for _, want := range []string{
		"written in claude · sess-wro",
		"wrote this line: conn.closeWithoutDrainingInflightRequests()",
		"user turn (2m before edit): добавь первоначальный shutdown",
		"why, in full: deja ctx sess-wro",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose output missing %q:\n%s", want, prose)
		}
	}
}

func TestOriginReportsUncertainOnOverlappingSessionsAndStaleWrites(t *testing.T) {
	hermeticEnv(t)
	repo, git := initOriginTestRepo(t)
	t.Chdir(repo)

	fileA := filepath.Join(repo, "internal", "pool", "a.go")
	fileB := filepath.Join(repo, "internal", "pool", "b.go")
	lineA := "\talphaConnectionPoolInitializerForShards()\n"
	lineB := "\tbetaConnectionPoolInitializerForShards()\n"

	if err := os.WriteFile(fileA, []byte("package pool\nfunc a() {\n"+lineA+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("package pool\nfunc b() {\n"+lineB+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-18T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-18T12:00:00Z"}, "commit", "-qm", "feat: add a and b")
	shaOverlap := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	// Commit 2: committed 10 days after the session wrote it -> uncertain (stale write gap).
	fileC := filepath.Join(repo, "internal", "pool", "c.go")
	lineC := "\tgammaConnectionPoolInitializerForShards()\n"
	if err := os.WriteFile(fileC, []byte("package pool\nfunc c() {\n"+lineC+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(nil, "add", ".")
	git([]string{"GIT_AUTHOR_DATE=2026-09-29T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-29T12:00:00Z"}, "commit", "-qm", "feat: add c")
	shaStale := strings.TrimSpace(git(nil, "rev-parse", "HEAD"))

	projDir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), nonAlnum.ReplaceAllString(repo, "-"))
	writeClaudeFixture(t, filepath.Join(projDir, "s-alpha.jsonl"), "s-alpha", []string{
		fixtureRow("s-alpha", "user", "2026-09-18T11:50:00Z", repo, "write a.go"),
		fixtureRow("s-alpha", "assistant", "2026-09-18T11:51:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wa", "name": "Write",
			"input": map[string]any{"file_path": fileA, "content": "package pool\nfunc a() {\n" + lineA + "}\n"},
		}}),
	})
	writeClaudeFixture(t, filepath.Join(projDir, "s-beta.jsonl"), "s-beta", []string{
		fixtureRow("s-beta", "user", "2026-09-18T11:52:00Z", repo, "write b.go and c.go"),
		fixtureRow("s-beta", "assistant", "2026-09-18T11:53:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wb", "name": "Write",
			"input": map[string]any{"file_path": fileB, "content": "package pool\nfunc b() {\n" + lineB + "}\n"},
		}}),
		fixtureRow("s-beta", "assistant", "2026-09-18T11:54:00Z", repo, []any{map[string]any{
			"type": "tool_use", "id": "wc", "name": "Write",
			"input": map[string]any{"file_path": fileC, "content": "package pool\nfunc c() {\n" + lineC + "}\n"},
		}}),
	})

	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Overlapping sessions on shaOverlap -> uncertain, 2 candidates.
	var buf bytes.Buffer
	if err := runOrigin(dir, []string{shaOverlap, "--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	var env originEnvelopeJSON
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Commits[0].Status != originStatusUncertain || !env.Commits[0].Uncertain || env.Commits[0].Attributed || len(env.Commits[0].Candidates) != 2 {
		t.Fatalf("overlap commit = %+v", env.Commits[0])
	}

	// Stale write (>7 days before commit) on shaStale -> uncertain.
	buf.Reset()
	if err := runOrigin(dir, []string{shaStale, "--json"}, &buf); err != nil {
		t.Fatal(err)
	}
	env = originEnvelopeJSON{}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Commits[0].Status != originStatusUncertain || !env.Commits[0].Uncertain || env.Commits[0].Attributed {
		t.Fatalf("stale commit = %+v", env.Commits[0])
	}
	if !strings.Contains(env.Commits[0].Why, "11d before the commit") {
		t.Errorf("why = %q, want 11d gap explanation", env.Commits[0].Why)
	}

	// Prose rendering of uncertain commit.
	buf.Reset()
	if err := runOrigin(dir, []string{shaStale}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "uncertain:") || !strings.Contains(buf.String(), "closest session:") {
		t.Errorf("uncertain prose:\n%s", buf.String())
	}
}

func TestParseOriginValidation(t *testing.T) {
	if o, err := parseOrigin(nil); err != nil || o.Rev != "HEAD" || o.Limit != originDefaultLimit {
		t.Errorf("default parseOrigin = %+v, %v", o, err)
	}
	if o, err := parseOrigin([]string{"--limit=5", "--harness=claude", "--project=app", "--", "main~5..main"}); err != nil ||
		o.Limit != 5 || o.Harness != "claude" || o.Project != "app" || o.Rev != "main~5..main" {
		t.Errorf("parsed = %+v, %v", o, err)
	}
	for _, bad := range [][]string{
		{"--unknown"},
		{"--limit", "0"},
		{"--limit", "999"},
		{"--harness"},
		{"--project", "  "},
		{"HEAD", "main"},
		{"--", "HEAD", "main"},
	} {
		if _, err := parseOrigin(bad); err == nil {
			t.Errorf("parseOrigin(%v) succeeded, want error", bad)
		}
	}
}
